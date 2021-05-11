package http

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	gopath "path"
	"path/filepath"
	"strings"

	"github.com/mholt/archiver"
	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/files"
	"github.com/filebrowser/filebrowser/v2/fileutils"
	"github.com/filebrowser/filebrowser/v2/users"
)

func slashClean(name string) string {
	if name == "" || name[0] != '/' {
		name = "/" + name
	}
	return gopath.Clean(name)
}

func parseQueryFiles(r *http.Request, f *files.FileInfo, _ *users.User) ([]string, error) {
	var fileSlice []string
	names := strings.Split(r.URL.Query().Get("files"), ",")

	if len(names) == 0 {
		fileSlice = append(fileSlice, f.Path)
	} else {
		for _, name := range names {
			name, err := url.QueryUnescape(strings.Replace(name, "+", "%2B", -1)) //nolint:govet
			if err != nil {
				return nil, err
			}

			name = slashClean(name)
			fileSlice = append(fileSlice, filepath.Join(f.Path, name))
		}
	}

	return fileSlice, nil
}

//nolint: goconst
func parseQueryAlgorithm(r *http.Request) (string, archiver.Writer, error) {
	// TODO: use enum
	switch r.URL.Query().Get("algo") {
	case "zip", "true", "":
		return ".zip", archiver.NewZip(), nil
	case "tar":
		return ".tar", archiver.NewTar(), nil
	case "targz":
		return ".tar.gz", archiver.NewTarGz(), nil
	case "tarbz2":
		return ".tar.bz2", archiver.NewTarBz2(), nil
	case "tarxz":
		return ".tar.xz", archiver.NewTarXz(), nil
	case "tarlz4":
		return ".tar.lz4", archiver.NewTarLz4(), nil
	case "tarsz":
		return ".tar.sz", archiver.NewTarSz(), nil
	default:
		return "", nil, errors.New("format not implemented")
	}
}

func setContentDisposition(w http.ResponseWriter, r *http.Request, file *files.FileInfo) {
	if r.URL.Query().Get("inline") == "true" {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		// As per RFC6266 section 4.3
		w.Header().Set("Content-Disposition", "attachment; filename*=utf-8''"+url.PathEscape(file.Name))
	}
}

var rawHandlerForUnzipping = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	if !d.user.Perm.Download {
		return http.StatusAccepted, nil
	}

	file, err := files.NewFileInfo(files.FileOptions{
		Fs:      d.user.Fs,
		Path:    r.URL.Path,
		Modify:  d.user.Perm.Modify,
		Expand:  false,
		Checker: d,
	})

	if err != nil {
		return errToStatus(err), err
	}

	if !file.IsDir {
		fd, err := file.Fs.Open(file.Path)
		if err != nil {
			return http.StatusInternalServerError, err
		}
		defer fd.Close()
		server, err := d.store.Settings.GetServer()
		if err != nil {
			return errToStatus(err), err
		}

		err = unzip(server.Root+file.Path, strings.ReplaceAll(server.Root+file.Path, "/"+file.Name, ""))
		if err != nil {
			return errToStatus(err), err
		}
		return http.StatusOK, nil
	}
	return 0, err
})

var rawHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	if !d.user.Perm.Download {
		return http.StatusAccepted, nil
	}

	file, err := files.NewFileInfo(files.FileOptions{
		Fs:         d.user.Fs,
		Path:       r.URL.Path,
		Modify:     d.user.Perm.Modify,
		Expand:     false,
		ReadHeader: d.server.TypeDetectionByHeader,
		Checker:    d,
	})
	if err != nil {
		return errToStatus(err), err
	}

	if files.IsNamedPipe(file.Mode) {
		setContentDisposition(w, r, file)
		return 0, nil
	}

	if !file.IsDir {
		return rawFileHandler(w, r, file)
	}

	return rawDirHandler(w, r, d, file)
})

func addFile(ar archiver.Writer, d *data, path, commonPath string) error {
	// Checks are always done with paths with "/" as path separator.
	path = strings.Replace(path, "\\", "/", -1)
	if !d.Check(path) {
		return nil
	}

	info, err := d.user.Fs.Stat(path)
	if err != nil {
		return err
	}

	var (
		file          afero.File
		arcReadCloser = ioutil.NopCloser(&bytes.Buffer{})
	)
	if !files.IsNamedPipe(info.Mode()) {
		file, err = d.user.Fs.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		arcReadCloser = file
	}

	if path != commonPath {
		filename := strings.TrimPrefix(path, commonPath)
		filename = strings.TrimPrefix(filename, "/")
		err = ar.Write(archiver.File{
			FileInfo: archiver.FileInfo{
				FileInfo:   info,
				CustomName: filename,
			},
			ReadCloser: arcReadCloser,
		})
		if err != nil {
			return err
		}
	}

	if info.IsDir() {
		names, err := file.Readdirnames(0)
		if err != nil {
			return err
		}

		for _, name := range names {
			err = addFile(ar, d, filepath.Join(path, name), commonPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func rawDirHandler(w http.ResponseWriter, r *http.Request, d *data, file *files.FileInfo) (int, error) {
	filenames, err := parseQueryFiles(r, file, d.user)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	extension, ar, err := parseQueryAlgorithm(r)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	name := file.Name
	if name == "." || name == "" {
		name = "archive"
	}
	name += extension
	w.Header().Set("Content-Disposition", "attachment; filename*=utf-8''"+url.PathEscape(name))

	err = ar.Create(w)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	defer ar.Close()

	commonDir := fileutils.CommonPrefix('/', filenames...)

	for _, fname := range filenames {
		err = addFile(ar, d, fname, commonDir)
		if err != nil {
			return http.StatusInternalServerError, err
		}
	}

	return 0, nil
}

func rawFileHandler(w http.ResponseWriter, r *http.Request, file *files.FileInfo) (int, error) {
	fd, err := file.Fs.Open(file.Path)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	defer fd.Close()

	setContentDisposition(w, r, file)

	http.ServeContent(w, r, file.Name, file.ModTime, fd)
	return 0, nil
}

func unzip(src, dest string) error {
	log.Println("[INFO] Starting to unzip. Src:", src, ". Destination:", dest)

	r, err := zip.OpenReader(src)
	if err != nil {
		log.Println("[ERROR] Failed opening src zip file while unzipping. Msg:", err.Error())
		return err
	}

	defer r.Close()

	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			log.Println("[ERROR] Failed opening file while unzipping. Msg:", err.Error())
			return err
		}

		defer rc.Close()

		fpath := filepath.Join(dest, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, 0775)
		} else {
			var fdir string
			if lastIndex := strings.LastIndex(fpath, string(os.PathSeparator)); lastIndex > -1 {
				fdir = fpath[:lastIndex]
			}

			err = os.MkdirAll(fdir, 0775)
			if err != nil {
				log.Println("[ERROR] Failed creating directory while unzipping. Msg:", err.Error())
				return err
			}
			f, err := os.OpenFile(
				fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				log.Println("[ERROR] Failed to OS open file while unzipping. Msg:", err.Error())
				return err
			}
			defer f.Close()

			_, err = io.Copy(f, rc)
			if err != nil {
				log.Println("[ERROR] Failed to IO copy while unzipping. Msg:", err.Error())
				return err
			}
			f.Close()
		}
		rc.Close()
	}

	log.Println("[INFO] Unzipping finished successfully")

	return nil
}
