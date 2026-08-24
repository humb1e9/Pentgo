package runtime

import (
	"pentgo/internal/project"
	projectcontext "pentgo/internal/project/context"
)

func (runtime *ProjectRuntime) ContextPreparer() projectcontext.ContextPreparer {
	return NewContextAssembler(runtime, project.DefaultConfig().Context, NewContextMeter(), nil)
}
