// Package web holds the citadel's browser client. The TypeScript sources live
// in src, Vite builds them into dist, and Dist embeds that build into the
// skylands binary so the citadel serves it with no other files on disk.
package web

import "embed"

// Dist is the built client. It holds only .gitkeep until "make web" runs,
// and the citadel serves a placeholder page in that case.
//
//go:embed all:dist
var Dist embed.FS
