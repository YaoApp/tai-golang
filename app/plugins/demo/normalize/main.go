package main

import (
	"encoding/json"
	"os"
	"strings"
)

type req struct {
	Input map[string]any `json:"input"`
	Args  map[string]any `json:"args"`
}

func main() {
	var r req
	if err := json.NewDecoder(os.Stdin).Decode(&r); err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"error": map[string]string{"code": "step_failed", "message": err.Error()}})
		return
	}
	text, _ := r.Input["text"].(string)
	if text == "" {
		text, _ = r.Input["msg"].(string)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"value": map[string]any{"text": strings.ToUpper(text), "normalized": true}})
}
