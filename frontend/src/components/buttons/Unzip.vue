<template>
  <button @click="unzip" :aria-label="$t('buttons.unzip')" :title="$t('buttons.unzip')" id="unzip-button" class="action">
<!--    <i class="fa fa-folder-open-o"></i>-->
    <span>Extract </span>
    <span v-if="selectedCount > 0" class="counter">{{ selectedCount }}</span>
  </button>
</template>

<script>
import {mapGetters, mapState} from 'vuex'
// import { files as api } from '@/api'

export default {
  name: 'unzip-button',
  computed: {
    ...mapState(['req', 'selected']),
    ...mapGetters(['isListing', 'selectedCount'])
  },
  methods: {
  }
}
</script>
