package web

import "embed"

// Files contains the browser control panel shipped with Net Switch.
//
//go:embed index.html styles.css i18n.js app.js logo.svg
var Files embed.FS
