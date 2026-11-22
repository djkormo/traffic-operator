package version

import "fmt"

var (
	// BuildTime is a time label of the moment when the binary was built
	BuildTime = "unset"
	// Commit is a last commit hash at the moment when the binary was built
	Commit = "unset"
	// Release is a semantic version of current build
	Release     = "unset"
	Application = "traffic-operator"
)

// Info contains versioning information
type Info struct {
	Application string `json:"application"`
	BuildTime   string `json:"buildTime"`
	Commit      string `json:"commit"`
	Release     string `json:"release"`
}

// Get returns version information
func Get() Info {
	return Info{
		Application: Application,
		BuildTime:   BuildTime,
		Commit:      Commit,
		Release:     Release,
	}
}

// String returns formatted version details
func (i Info) String() string {
	return fmt.Sprintf("%s %s (commit: %s, build time: %s)", i.Application, i.Release, i.Commit, i.BuildTime)
}
