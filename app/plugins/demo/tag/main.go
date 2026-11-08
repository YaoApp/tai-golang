package main

import (
	"encoding/json"
	"os"
)

type request struct {
	Input map[string]any `json:"input"`
}

func main() {
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"error": map[string]string{"code": "step_failed", "message": err.Error()}})
		return
	}
	if req.Input == nil {
		req.Input = map[string]any{}
	}
	req.Input["source"] = "wasm"
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"value": req.Input})
}
