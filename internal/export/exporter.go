package export

import "hali/internal/runtime"

type Exporter interface {
	Name() string
	Supports(model Model) bool
	Export(model Model, rt runtime.Runtime) error
}
