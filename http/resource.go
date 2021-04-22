package http

import (
	"bufio"
	"fmt"
	"github.com/spf13/afero"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/filebrowser/filebrowser/v2/errors"
	"github.com/filebrowser/filebrowser/v2/files"
	"github.com/filebrowser/filebrowser/v2/fileutils"
)

var resourceGetHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	file, err := files.NewFileInfo(files.FileOptions{
		Fs:         d.user.Fs,
		Path:       r.URL.Path,
		Modify:     d.user.Perm.Modify,
		Expand:     true,
		ReadHeader: d.server.TypeDetectionByHeader,
		Checker:    d,
	})
	if err != nil {
		return errToStatus(err), err
	}

	if file.IsDir {
		file.Listing.Sorting = d.user.Sorting
		file.Listing.ApplySort()
		return renderJSON(w, r, file)
	}

	if checksum := r.URL.Query().Get("checksum"); checksum != "" {
		err := file.Checksum(checksum)
		if err == errors.ErrInvalidOption {
			return http.StatusBadRequest, nil
		} else if err != nil {
			return http.StatusInternalServerError, err
		}

		// do not waste bandwidth if we just want the checksum
		file.Content = ""
	}

	return renderJSON(w, r, file)
})

func resourceDeleteHandler(fileCache FileCache) handleFunc {
	return withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
		if r.URL.Path == "/" || !d.user.Perm.Delete {
			return http.StatusForbidden, nil
		}

		file, err := files.NewFileInfo(files.FileOptions{
			Fs:         d.user.Fs,
			Path:       r.URL.Path,
			Modify:     d.user.Perm.Modify,
			Expand:     true,
			ReadHeader: d.server.TypeDetectionByHeader,
			Checker:    d,
		})
		if err != nil {
			return errToStatus(err), err
		}

		// delete thumbnails
		for _, previewSizeName := range PreviewSizeNames() {
			size, _ := ParsePreviewSize(previewSizeName)
			if err := fileCache.Delete(r.Context(), previewCacheKey(file.Path, size)); err != nil { //nolint:govet
				return errToStatus(err), err
			}
		}

		err = d.RunHook(func() error {
			return d.user.Fs.RemoveAll(r.URL.Path)
		}, "delete", r.URL.Path, "", d.user)

		if err != nil {
			return errToStatus(err), err
		}

		return http.StatusOK, nil
	})
}

var resumableUpload = func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {

	server, err := d.store.Settings.GetServer()
	if err != nil {
		http.Error(w, "Server config not found", http.StatusInternalServerError)
	}

	tempFolder := server.Root + "/.temp/"
	//
	//if _, err := os.Stat(tempFolder); os.IsExist(err) {
	//	os.RemoveAll(tempFolder)
	//}

	if _, err := os.Stat(tempFolder); os.IsNotExist(err) {
		os.Mkdir(tempFolder, os.ModePerm)
	}

	switch r.Method {
	case "GET":
		resumableIdentifier, _ := r.URL.Query()["resumableIdentifier"]
		resumableChunkNumber, _ := r.URL.Query()["resumableChunkNumber"]
		path := fmt.Sprintf("%s%s", tempFolder, resumableIdentifier[0])
		relativeChunk := fmt.Sprintf("%s%s%s%s", path, "/", "part", resumableChunkNumber[0])

		if _, err := os.Stat(path); os.IsNotExist(err) {
			os.Mkdir(path, os.ModePerm)
		}

		if _, err := os.Stat(relativeChunk); os.IsNotExist(err) {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusMethodNotAllowed)
		} else {
			os.RemoveAll(tempFolder)
			http.Error(w, "Chunk already exist", http.StatusCreated)
		}

	default:
		r.ParseMultipartForm(10 << 20)
		file, _, err := r.FormFile("file")
		if err != nil {
			print(err.Error())
			return 0, nil
		}
		defer file.Close()
		resumableIdentifier, _ := r.URL.Query()["resumableIdentifier"]
		resumableChunkNumber, _ := r.URL.Query()["resumableChunkNumber"]
		path := fmt.Sprintf("%s%s", tempFolder, resumableIdentifier[0])
		relativeChunk := fmt.Sprintf("%s%s%s%s", path, "/", "part", resumableChunkNumber[0])

		if _, err := os.Stat(path); os.IsNotExist(err) {
			os.Mkdir(path, os.ModePerm)
		}

		f, err := os.OpenFile(relativeChunk, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			print(err.Error())
		}
		defer f.Close()
		io.Copy(f, file)

		/*
			If it is the last chunk, trigger the recombination of chunks
		*/
		resumableTotalChunks, _ := r.URL.Query()["resumableTotalChunks"]
		//resumableFilename, _ := r.URL.Query()["resumableFilename"]
		resumableRelativePath, _ := r.URL.Query()["resumableRelativePath"]
		//fmt.Println("Relative path: "+resumableRelativePath[0])

		current, err := strconv.Atoi(resumableChunkNumber[0])
		total, err := strconv.Atoi(resumableTotalChunks[0])
		if current == total {
			err = combineChunks(uint64(total), path+"/part", resumableRelativePath[0], server.Root)
			if err != nil {
				return http.StatusInternalServerError, err
			}
			err = os.Remove(path)
			if err != nil {
				fmt.Println(err)
				//os.Exit(1)
			}
			os.Remove(tempFolder)
		}

	}
	return renderJSON(w, r, nil)
}

func combineChunks(totalPartsNum uint64, path string, fileName string, rootDir string) error {

	dir := rootDir + "/"
	fileName = dir + fileName

	log.Println("Combining chunks for:", fileName)

	if _, err := os.Stat(filepath.Dir(fileName)); os.IsNotExist(err) {
		os.Mkdir(filepath.Dir(fileName), os.ModePerm)
	}

	_, err := os.Create(fileName)

	if err != nil {
		fmt.Println(err)
		return err
	}

	file, err := os.OpenFile(fileName, os.O_APPEND|os.O_WRONLY, os.ModeAppend)

	if err != nil {
		fmt.Println(err)
		return err
	}

	var writePosition int64 = 0
	for j := uint64(0) + 1; j <= totalPartsNum; j++ {

		//read a chunk
		currentChunkFileName := path + strconv.FormatUint(j, 10)

		newFileChunk, err := os.Open(currentChunkFileName)

		if err != nil {
			fmt.Println(err)
			return err
		}

		defer newFileChunk.Close()

		chunkInfo, err := newFileChunk.Stat()

		if err != nil {
			fmt.Println(err)
			return err
		}

		// calculate the bytes size of each chunk
		// we are not going to rely on previous data and constant

		var chunkSize int64 = chunkInfo.Size()
		chunkBufferBytes := make([]byte, chunkSize)

		//fmt.Println("Appending at position : [", writePosition, "] bytes")
		writePosition = writePosition + chunkSize

		// read into chunkBufferBytes
		reader := bufio.NewReader(newFileChunk)
		_, err = reader.Read(chunkBufferBytes)

		if err != nil {
			fmt.Println(err)
			return err
		}

		// DON't USE ioutil.WriteFile -- it will overwrite the previous bytes!
		// write/save buffer to disk
		//ioutil.WriteFile(fileName, chunkBufferBytes, os.ModeAppend)

		_, err = file.Write(chunkBufferBytes)

		if err != nil {
			fmt.Println(err)
			return err
		}

		file.Sync() //flush to disk

		// free up the buffer for next cycle
		// should not be a problem if the chunk size is small, but
		// can be resource hogging if the chunk size is huge.
		// also a good practice to clean up your own plate after eating

		chunkBufferBytes = nil // reset or empty our buffer

		err = os.Remove(currentChunkFileName)
		if err != nil {
			fmt.Println(err)
			return err
		}
	}

	log.Println("All chunks combined for:", fileName)

	// now, we close the fileName
	file.Close()

	return nil
}

var resourcePostPutHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	if !d.user.Perm.Create && r.Method == http.MethodPost {
		return http.StatusForbidden, nil
	}

	if !d.user.Perm.Modify && r.Method == http.MethodPut {
		return http.StatusForbidden, nil
	}

	defer func() {
		_, _ = io.Copy(ioutil.Discard, r.Body)
	}()

	// For directories, only allow POST for creation.
	if strings.HasSuffix(r.URL.Path, "/") {
		if r.Method == http.MethodPut {
			return http.StatusMethodNotAllowed, nil
		}

		err := d.user.Fs.MkdirAll(r.URL.Path, 0775)
		return errToStatus(err), err
	}

	if r.Method == http.MethodPost && r.URL.Query().Get("override") != "true" {
		if _, err := d.user.Fs.Stat(r.URL.Path); err == nil {
			return http.StatusConflict, nil
		}
	}

	action := "upload"
	if r.Method == http.MethodPut {
		action = "save"
	}

	err := d.RunHook(func() error {
		dir, _ := path.Split(r.URL.Path)
		err := d.user.Fs.MkdirAll(dir, 0775)
		if err != nil {
			return err
		}

		file, err := d.user.Fs.OpenFile(r.URL.Path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0775)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(file, r.Body)
		if err != nil {
			return err
		}

		// Gets the info about the file.
		info, err := file.Stat()
		if err != nil {
			return err
		}

		etag := fmt.Sprintf(`"%x%x"`, info.ModTime().UnixNano(), info.Size())
		w.Header().Set("ETag", etag)
		return nil
	}, action, r.URL.Path, "", d.user)

	if err != nil {
		_ = d.user.Fs.RemoveAll(r.URL.Path)
	}

	return errToStatus(err), err
})

var resourcePatchHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	src := r.URL.Path
	dst := r.URL.Query().Get("destination")
	action := r.URL.Query().Get("action")
	dst, err := url.QueryUnescape(dst)
	if err != nil {
		return errToStatus(err), err
	}
	if dst == "/" || src == "/" {
		return http.StatusForbidden, nil
	}
	if err = checkParent(src, dst); err != nil {
		return http.StatusBadRequest, err
	}

	override := r.URL.Query().Get("override") == "true"
	rename := r.URL.Query().Get("rename") == "true"
	if !override && !rename {
		if _, err = d.user.Fs.Stat(dst); err == nil {
			return http.StatusConflict, nil
		}
	}
	if rename {
		dst = addVersionSuffix(dst, d.user.Fs)
	}

	err = d.RunHook(func() error {
		switch action {
		// TODO: use enum
		case "copy":
			if !d.user.Perm.Create {
				return errors.ErrPermissionDenied
			}

			return fileutils.Copy(d.user.Fs, src, dst)
		case "rename":
			if !d.user.Perm.Rename {
				return errors.ErrPermissionDenied
			}
			src = path.Clean("/" + src)
			dst = path.Clean("/" + dst)

			return fileutils.MoveFile(d.user.Fs, src, dst)
		default:
			return fmt.Errorf("unsupported action %s: %w", action, errors.ErrInvalidRequestParams)
		}
	}, action, src, dst, d.user)

	return errToStatus(err), err
})

func checkParent(src, dst string) error {
	rel, err := filepath.Rel(src, dst)
	if err != nil {
		return err
	}

	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "../") && rel != ".." && rel != "." {
		return errors.ErrSourceIsParent
	}

	return nil
}

func addVersionSuffix(source string, fs afero.Fs) string {
	counter := 1
	dir, name := path.Split(source)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)

	for {
		if _, err := fs.Stat(source); err != nil {
			break
		}
		renamed := fmt.Sprintf("%s(%d)%s", base, counter, ext)
		source = path.Join(dir, renamed)
		counter++
	}

	return source
}
