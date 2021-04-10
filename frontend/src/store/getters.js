const getters = {
  isLogged: state => state.user !== null,
  isFiles: state => !state.loading && state.route.name === 'Files',
  isListing: (state, getters) => getters.isFiles && state.req.isDir,
  isEditor: (state, getters) => getters.isFiles && (state.req.type === 'text' || state.req.type === 'textImmutable'),
  isPreview: state => state.previewMode,
  isSharing: state =>  !state.loading && state.route.name === 'Share',
  selectedCount: state => state.selected.length,
  progress : state => {
    return state.progress;
  }
}

export default getters
