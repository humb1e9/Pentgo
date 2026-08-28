package storage

import "time"

func timeValue(value time.Time) int64 { return value.UTC().UnixNano() }
func parseTime(value int64) time.Time { return time.Unix(0, value).UTC() }
func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
