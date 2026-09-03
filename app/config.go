package app

import "pentgo/internal/agent"

// Config is the shared runtime and on-disk configuration schema.
type Config = agent.Config

func Default() Config { return agent.DefaultConfig() }
