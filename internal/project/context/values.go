package context

import "time"

func timeValue(value time.Time) int64 { return value.UTC().UnixNano() }
