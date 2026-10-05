package database

import "fmt"

// DefaultTailnetID is the tailnet assigned to rows that predate per-tailnet
// storage, and the only tailnet the running process reads and writes.
const DefaultTailnetID = "default"

func checkTailnetID(tailnetID string) error {
	if tailnetID == "" {
		return fmt.Errorf("tailnet id is required")
	}
	return nil
}
