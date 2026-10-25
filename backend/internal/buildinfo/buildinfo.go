// Package buildinfo exposes metadata injected by the release build.
package buildinfo

var (
	Version  = "dev"
	Revision = ""
	Created  = ""
)

type Info struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
	Created  string `json:"created"`
}

func Current() Info {
	return Info{Version: Version, Revision: Revision, Created: Created}
}
