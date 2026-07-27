package agent

import (
	"encoding/json"
	"strings"
)

// A plugin CLI returns either the documented object envelope or, depending on version, the bare array it would have carried.
func decodeCLIList[T any](stdout string, envelope any) (items []T, isArray bool, err error) {
	trimmed := strings.TrimSpace(stdout)
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
			return nil, true, err
		}
		return items, true, nil
	}
	if err := json.Unmarshal([]byte(trimmed), envelope); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}
