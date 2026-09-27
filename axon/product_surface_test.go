package axon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var forbiddenProductExports = []string{
	"Audio",
	"Voice",
	"MCP",
	"ToolAdapter",
	"RemoteControl",
	"AbilityDispatch",
	"Federation",
	"Directory",
	"Orchestrator",
	"Deploy",
	"Install",
	"Uninstall",
	"PhaseReceipt",
	"APIKeyResource",
	"AgentSkill",
	"FilesResource",
	"PagesResource",
	"DeviceNode",
}

func productExportFindings(root string) ([]string, error) {
	findings := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch declaration := node.(type) {
			case *ast.TypeSpec:
				if ast.IsExported(declaration.Name.Name) {
					findings = appendProductExportFinding(
						findings,
						path,
						declaration.Name.Name,
					)
				}
			case *ast.FuncDecl:
				if ast.IsExported(declaration.Name.Name) {
					findings = appendProductExportFinding(
						findings,
						path,
						declaration.Name.Name,
					)
				}
			}
			return true
		})
		return nil
	})
	return findings, err
}

func appendProductExportFinding(findings []string, path string, name string) []string {
	for _, forbidden := range forbiddenProductExports {
		if strings.Contains(name, forbidden) {
			return append(findings, path+": "+name)
		}
	}
	return findings
}

func TestCanonicalGoSurfaceRejectsProductExports(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	findings, err := productExportFindings(filepath.Dir(currentFile))
	if err != nil {
		t.Fatalf("scan product exports: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("canonical Go package exports product concepts:\n%s", strings.Join(findings, "\n"))
	}
}

func TestProductSurfaceScannerRejectsNegativeFixture(t *testing.T) {
	root := t.TempDir()
	source := []byte("package fixture\n\ntype VoiceCall struct{}\n")
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), source, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	findings, err := productExportFindings(root)
	if err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0], "VoiceCall") {
		t.Fatalf("scanner accepted product fixture: %v", findings)
	}
}
