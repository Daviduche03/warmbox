// Package release knows where warmbox publishes things.
//
// Two releases, deliberately separate: the versioned one that carries the CLI,
// and a fixed tag that carries the guest images. Both the CLI and the daemon need
// these addresses — the CLI to fetch, the daemon to offer the Images page — so
// they live here rather than inside the command.
package release

import "fmt"

const (
	// Repo is the GitHub owner/name warmbox is published from.
	Repo = "Daviduche03/warmbox"

	// ImagesTag is the fixed release tag holding the guest images. It is not the
	// binary version on purpose: a guest changes far less often than the CLI, and
	// tying the two together meant re-uploading hundreds of MB on every release.
	// `warmbox setup` asks for this tag, so a new binary finds images that are
	// already published.
	ImagesTag = "images"
)

// ImageAsset is the published file name for one image on one architecture.
func ImageAsset(name, arch string) string {
	return fmt.Sprintf("warmbox-image-%s-%s.tar.zst", name, arch)
}

// ImageURL is the download address for that asset.
func ImageURL(name, arch string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s",
		Repo, ImagesTag, ImageAsset(name, arch))
}

// SourceURL is the source tarball for a tag, used when an image has to be built
// on this machine because nothing is published for the platform.
func SourceURL(tag string) string {
	return fmt.Sprintf("https://github.com/%s/archive/refs/tags/%s.tar.gz", Repo, tag)
}

// PageURL is the human-facing release page for a tag.
func PageURL(tag string) string {
	return fmt.Sprintf("https://github.com/%s/releases/tag/%s", Repo, tag)
}
