package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteNodeInfo(t *testing.T) {
	logDir := t.TempDir()
	originalCollector := collector
	collector = &PodLogCollector{logDir: logDir}
	t.Cleanup(func() { collector = originalCollector })

	err := WriteNodeInfo("node-b", "bc", map[string]string{
		"Grandmaster Clock": "node-a",
		"Clock Under Test":  "node-b",
	})
	if err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(logDir, "NODE_INFO.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{"Node Under Test: node-b", "Clock Under Test: node-b", "Grandmaster Clock: node-a"} {
		if !strings.Contains(text, want) {
			t.Errorf("NODE_INFO.txt missing %q", want)
		}
	}
}
