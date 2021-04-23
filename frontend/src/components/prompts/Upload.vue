<template>
  <div class="card floating">
    <div class="card-title">
      <h2>{{ $t('prompts.upload') }}</h2>
    </div>

    <div class="card-content">
      <p>{{ $t('prompts.uploadMessage') }}</p>
    </div>

    <div class="card-action full">
      <div @click="uploadFile" class="action">
        <i class="material-icons">insert_drive_file</i>
        <div class="title">File</div>
      </div>
      <div @click="uploadFolder" class="action">
        <i class="material-icons">folder</i>
        <div class="title">Folder</div>
      </div>
    </div>
  </div>
</template>

<script>

import Resumable from 'resumablejs';
import { mapMutations } from 'vuex';
import { baseURL, chunkSizeFactor,simultaneousUploads } from '@/utils/constants';

export default {
  name: 'upload',
  methods: {
    ...mapMutations(['setReload', 'setProgress']),

    uploadFile: function () {
      document.getElementById('upload-input').click()

      console.log("route: ")
      console.log(this.$route.params.pathMatch);
      var self = this;
      var r = new Resumable({
        target: `${baseURL}/api/chunk-upload`,
        chunkSize: chunkSizeFactor * 1024 * 1024,
        simultaneousUploads: simultaneousUploads,
        query: {subPath : this.$route.params.pathMatch}
      });
      r.assignBrowse(document.getElementById('upload-input'));

      if (!r.support) location.href = '/some-old-crappy-uploader';

      r.on('fileAdded', function (file) {
        console.log("fileAdded")
        console.log(file)

        let conflict = false;
        let req = self.$store.state.req
        console.log(req)
        let fileName = file.relativePath
        for (let item of req.items) {
          if (item.name === fileName && !item.isDir)
            conflict = true
        }
        if (!conflict){
          self.$store.commit('closeHovers')
          r.upload()
        }
        else
          console.log("Conflict detected by custom function")

        if (conflict) {
          let result = confirm("Do you want to replace the file?");
          if (result) {
            self.$store.commit('closeHovers')
            console.log("Overwrite permitted")
            console.log("Overwriting")
            r.upload()
          } else {
            self.$store.commit('closeHovers')
            console.log("Permission Denied!")
            r.removeFile(file)
          }
        }
      });
      r.on('progress', function () {
        self.progress = r.progress() * 100
        self.$store.commit('setProgress', self.progress)
      })
      r.on('fileSuccess', function (file, message) {
        console.log("Success")
        self.$store.commit('setReload', true)
        self.$store.commit('setProgress', 0)
        console.log(file, message)
      });
      r.on('fileError', function (file, message) {
        console.log("Failed ")
        console.log(file, message)
      });
    },
    uploadFolder: function () {
      document.getElementById('upload-folder-input').value = ''
      document.getElementById('upload-folder-input').click()

      var self = this;
      var r = new Resumable({
        target: `${baseURL}/api/chunk-upload`,
        chunkSize: chunkSizeFactor * 1024 * 1024,
        simultaneousUploads: simultaneousUploads,
        query: {subPath : this.$route.params.pathMatch}
      });
      r.assignBrowse(document.getElementById('upload-folder-input'), true);

      if (!r.support) location.href = '/some-old-crappy-uploader';

      r.on('filesAdded', function (files, filesSkipped) {
        console.log("filesAdded")
        console.log(files)
        console.log(filesSkipped)
        let conflict = false;
        let req = self.$store.state.req
        console.log(req)
        for (let i = 0; i < files.length; i++) {
          let folderName = self.dirName(files[i].relativePath)
          console.log(folderName)
          for (let item of req.items) {
            if (item.name === folderName && item.isDir) {
              conflict = true
              break
            }
          }
        }

        if (!conflict){
          self.$store.commit('closeHovers')
          r.upload()
        }
        else
          console.log("Conflict detected by custom function")

        if (conflict) {
          let result = confirm("Do you want to replace the folder?");
          if (result) {
            self.$store.commit('closeHovers')
            console.log("Overwrite permitted")
            console.log("Overwriting")
            r.upload()
          } else {
            self.$store.commit('closeHovers')
            console.log("Permission Denied!")
            r.cancel()
          }
        }
      });
      r.on('progress', function () {
        self.progress = r.progress() * 100
        self.$store.commit('setProgress', self.progress)
        // console.log(`Progress: ${self.progress}`)
      })
      r.on('fileSuccess', function (file, message) {
        console.log("File Success")
        console.log(file, message)
      });
      r.on('fileError', function (file, message) {
        console.log("Failed ")
        console.log(file, message)
      });

      r.on('complete', function () {
        console.log("complete")
        self.$store.commit('setReload', true)
        self.$store.commit('setProgress', 0)
      });
    },
    dirName: function (x) {
      let root = '';
      for (let i = 0; i < x.length; i++) {
        if (x[i] === "/")
          break;
        else
          root += x[i]
      }
      return root;
    }
  }
}
</script>
