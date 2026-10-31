package workspace

import (
	"encoding/json"
	"testing"
)

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()
	if p.SizeThreshold != 1<<20 {
		t.Errorf("SizeThreshold = %d, want %d", p.SizeThreshold, 1<<20)
	}
	if len(p.TextExts) == 0 {
		t.Error("TextExts should not be empty")
	}
	if len(p.BinaryExts) == 0 {
		t.Error("BinaryExts should not be empty")
	}
}

func TestChannelForPathTextExt(t *testing.T) {
	p := DefaultPolicy()
	cases := []struct {
		path string
		want ChannelType
	}{
		{"src/main.go", ChannelLight},
		{"config.json", ChannelLight},
		{"README.md", ChannelLight},
		{"styles.css", ChannelLight},
		{"index.html", ChannelLight},
		{"script.py", ChannelLight},
		{"Makefile.yaml", ChannelLight},
		{"data.csv", ChannelLight},
		{"query.sql", ChannelLight},
	}
	for _, tc := range cases {
		if got := p.channelForPath(tc.path, 100); got != tc.want {
			t.Errorf("channelForPath(%q) = %d, want %d", tc.path, got, tc.want)
		}
	}
}

func TestChannelForPathBinaryExt(t *testing.T) {
	p := DefaultPolicy()
	cases := []struct {
		path string
		want ChannelType
	}{
		{"image.png", ChannelHeavy},
		{"video.mp4", ChannelHeavy},
		{"archive.zip", ChannelHeavy},
		{"binary.exe", ChannelHeavy},
		{"lib.so", ChannelHeavy},
		{"doc.pdf", ChannelHeavy},
		{"module.wasm", ChannelHeavy},
		{"photo.jpg", ChannelHeavy},
	}
	for _, tc := range cases {
		if got := p.channelForPath(tc.path, 100); got != tc.want {
			t.Errorf("channelForPath(%q) = %d, want %d", tc.path, got, tc.want)
		}
	}
}

func TestChannelForPathUnknownExtSize(t *testing.T) {
	p := DefaultPolicy()

	// Unknown ext, small file → Light
	if got := p.channelForPath("data.xyz", 512); got != ChannelLight {
		t.Errorf("small unknown ext = %d, want Light", got)
	}

	// Unknown ext, large file → Heavy
	if got := p.channelForPath("data.xyz", 2<<20); got != ChannelHeavy {
		t.Errorf("large unknown ext = %d, want Heavy", got)
	}

	// Unknown ext, exactly at threshold → Light (not >)
	if got := p.channelForPath("data.xyz", 1<<20); got != ChannelLight {
		t.Errorf("threshold unknown ext = %d, want Light", got)
	}

	// Unknown ext, unknown size → Light
	if got := p.channelForPath("data.xyz", -1); got != ChannelLight {
		t.Errorf("unknown size = %d, want Light", got)
	}
}

func TestChannelForPathCaseInsensitive(t *testing.T) {
	p := DefaultPolicy()
	if got := p.channelForPath("IMAGE.PNG", 100); got != ChannelHeavy {
		t.Errorf("upper case .PNG = %d, want Heavy", got)
	}
	if got := p.channelForPath("FILE.GO", 100); got != ChannelLight {
		t.Errorf("upper case .GO = %d, want Light", got)
	}
}

func TestChannelForMeta(t *testing.T) {
	if got := channelForMeta(); got != ChannelLight {
		t.Errorf("channelForMeta = %d, want Light", got)
	}
}

func TestCommandKindCount(t *testing.T) {
	kinds := []string{
		// File ops (9)
		KindOpen, KindCreate, KindStat, KindReadDir, KindRemove,
		KindRemoveAll, KindRename, KindMkdirAll, KindSymlink,
		// Branch info + VC (8)
		KindBranchHead, KindBranchMeta, KindBranchUpdateMeta,
		KindBranchAcquireW, KindBranchReleaseW,
		KindCommit, KindDiff, KindRollback,
		// Workspace CRUD (10)
		KindWSCreate, KindWSOpen, KindWSList, KindWSDelete, KindWSUpdateMeta,
		KindWSGetCommit, KindWSHistory, KindWSFileHistory, KindWSBranchLog, KindWSGC,
		// Branch management (5)
		KindListBranches, KindCreateBranch, KindGetOrCreateBranch, KindDeleteBranch, KindMerge,
		// Watch (1)
		KindWatch,
	}
	if len(kinds) != 33 {
		t.Errorf("total kinds = %d, want 33", len(kinds))
	}

	// Check uniqueness
	seen := make(map[string]bool)
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("duplicate kind: %s", k)
		}
		seen[k] = true
	}
}

func TestErrorResponse(t *testing.T) {
	e := &ErrorResponse{Code: "NOT_FOUND", Message: "workspace not found"}
	if got := e.Error(); got != "NOT_FOUND: workspace not found" {
		t.Errorf("Error() = %q", got)
	}
}

func TestFileRequestJSON(t *testing.T) {
	req := FileRequest{WorkspaceID: "ws_1", Branch: "main", Path: "src/main.go"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var dec FileRequest
	if err := json.Unmarshal(data, &dec); err != nil {
		t.Fatal(err)
	}
	if dec.WorkspaceID != "ws_1" || dec.Branch != "main" || dec.Path != "src/main.go" {
		t.Errorf("round-trip mismatch: %+v", dec)
	}
}

func TestRenameRequestJSON(t *testing.T) {
	req := RenameRequest{WorkspaceID: "ws_1", Branch: "main", Old: "a.txt", New: "b.txt"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var dec RenameRequest
	if err := json.Unmarshal(data, &dec); err != nil {
		t.Fatal(err)
	}
	if dec.Old != "a.txt" || dec.New != "b.txt" {
		t.Errorf("round-trip mismatch: %+v", dec)
	}
}

func TestBranchMgmtRequestJSON(t *testing.T) {
	req := BranchMgmtRequest{
		WorkspaceID: "ws_1",
		Name:        "feature-x",
		From:        "main",
		Meta:        map[string]any{"key": "value"},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var dec BranchMgmtRequest
	if err := json.Unmarshal(data, &dec); err != nil {
		t.Fatal(err)
	}
	if dec.Name != "feature-x" || dec.From != "main" {
		t.Errorf("round-trip mismatch: %+v", dec)
	}
}

func TestWSCreateRequestJSON(t *testing.T) {
	req := WSCreateRequest{Path: "/data/project", Meta: Meta{Name: "proj", Owner: "u1"}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var dec WSCreateRequest
	if err := json.Unmarshal(data, &dec); err != nil {
		t.Fatal(err)
	}
	if dec.Path != "/data/project" || dec.Meta.Name != "proj" {
		t.Errorf("round-trip mismatch: %+v", dec)
	}
}

func TestWatchRequestJSON(t *testing.T) {
	req := WatchRequest{WorkspaceID: "ws_1", Branch: "main", Patterns: []string{"*.go", "*.ts"}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var dec WatchRequest
	if err := json.Unmarshal(data, &dec); err != nil {
		t.Fatal(err)
	}
	if len(dec.Patterns) != 2 {
		t.Errorf("patterns len = %d, want 2", len(dec.Patterns))
	}
}
