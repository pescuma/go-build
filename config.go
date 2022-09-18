package build

type BuilderConfig struct {
	BaseDir string

	MainFileNames []string

	// nil means all
	Archs []string

	GCO             bool
	PreserveSymbols bool
	BuildArgs       []string
	LDFlagsVars     map[string]string
}

func NewBuilderConfig() *BuilderConfig {
	result := &BuilderConfig{}

	result.MainFileNames = []string{"main.go"}

	result.Archs = []string{
		"darwin",
		"freebsd",
		"linux",
		"netbsd",
		"openbsd",
		"windows",
	}

	result.GCO = false
	result.PreserveSymbols = true
	result.BuildArgs = []string{"-trimpath"}
	result.LDFlagsVars = map[string]string{}

	return result
}
