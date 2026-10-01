// Package images carries the guest-image registry into the binary.
//
// The YAML files here are read twice: `warmbox image build` parses them from a
// checkout, and the daemon embeds this directory so it can list the images that
// exist but are not installed yet. Go cannot embed a path above its own package,
// so the data lives beside this file.
//
// Keeping the registry in the binary is deliberate. Asking GitHub what exists
// would make the Images page depend on the network and on a rate limit, and an
// asset listing only carries a file name — no "this one has no screen", no
// desktop or theme. The binary is what decides what it can boot, so it is the
// honest source for what it can offer.
package images

import "embed"

// FS is the image registry as it shipped with this build.

//go:embed *.yaml
var FS embed.FS
