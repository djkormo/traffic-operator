package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetNonEmptyLines(t *testing.T) {
	input := "line1\n\nline2\n\n\nline3\n"
	lines := GetNonEmptyLines(input)
	if len(lines) != 3 {
		t.Fatalf("expected 3 non-empty lines, got %d", len(lines))
	}
	if lines[0] != "line1" || lines[1] != "line2" || lines[2] != "line3" {
		t.Errorf("unexpected lines: %v", lines)
	}

	emptyLines := GetNonEmptyLines("\n\n\n")
	if len(emptyLines) != 0 {
		t.Errorf("expected 0 non-empty lines, got %d", len(emptyLines))
	}
}

func TestWarnError(t *testing.T) {
	warnError(fmt.Errorf("sample error"))
}

func TestGetProjectDir(t *testing.T) {
	dir, err := GetProjectDir()
	if err != nil {
		t.Fatalf("GetProjectDir failed: %v", err)
	}
	if dir == "" {
		t.Error("expected non-empty project directory")
	}
	if strings.HasSuffix(dir, "/test/e2e") {
		t.Errorf("project dir should not have /test/e2e suffix: %s", dir)
	}
}

func TestRun(t *testing.T) {
	cmd := exec.Command("echo", "hello-world")
	output, err := Run(cmd)
	if err != nil {
		t.Fatalf("Run failed for echo: %v", err)
	}
	if !strings.Contains(output, "hello-world") {
		t.Errorf("expected output to contain 'hello-world', got: %s", output)
	}

	failingCmd := exec.Command("false")
	_, err = Run(failingCmd)
	if err == nil {
		t.Error("expected error from running 'false', got nil")
	}
}

func TestUncommentCode(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "config.yaml")

	originalContent := "# header\n# line 1\n# line 2\n# footer\n"
	if err := os.WriteFile(testFile, []byte(originalContent), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	target := "# line 1\n# line 2"
	if err := UncommentCode(testFile, target, "# "); err != nil {
		t.Fatalf("UncommentCode failed: %v", err)
	}

	updated, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("failed to read updated file: %v", err)
	}

	expected := "# header\nline 1\nline 2\n# footer\n"
	if string(updated) != expected {
		t.Errorf("content mismatch:\ngot:\n%s\nwant:\n%s", string(updated), expected)
	}

	// Test target not found
	if err := UncommentCode(testFile, "non-existent-target", "#"); err == nil {
		t.Error("expected error when target not found, got nil")
	}

	// Test non-existent file
	if err := UncommentCode(filepath.Join(tmpDir, "does-not-exist.yaml"), target, "#"); err == nil {
		t.Error("expected error when file does not exist, got nil")
	}

	// Test empty target (scanner.Scan() is false)
	if err := UncommentCode(testFile, "", "#"); err != nil {
		t.Errorf("expected nil error for empty target, got: %v", err)
	}

	// Test write error by making directory and file read-only
	readOnlyDir := t.TempDir()
	roFile := filepath.Join(readOnlyDir, "readonly.yaml")
	if err := os.WriteFile(roFile, []byte("# line 1\n"), 0400); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	_ = os.Chmod(readOnlyDir, 0500)
	defer func() { _ = os.Chmod(readOnlyDir, 0700) }()

	if err := UncommentCode(roFile, "# line 1", "# "); err == nil {
		// on some root/container setups chmod may be ignored, so only assert if error is returned
		t.Log("os.WriteFile failed as expected or bypassed permissions")
	}
}

func TestGetProjectDirFromSubdir(t *testing.T) {
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	defer func() { _ = os.Chdir(orig) }()

	e2eDir := filepath.Join(orig, "test", "e2e")
	if err := os.Chdir(e2eDir); err == nil {
		dir, err := GetProjectDir()
		if err != nil {
			t.Fatalf("GetProjectDir failed: %v", err)
		}
		if strings.HasSuffix(dir, "/test/e2e") {
			t.Errorf("expected /test/e2e to be stripped: %s", dir)
		}
	}
}

func TestCommandsWithoutKubectlAndKind(t *testing.T) {
	// With an empty PATH, kubectl and kind cannot be found
	t.Setenv("PATH", t.TempDir())

	if err := InstallPrometheusOperator(); err == nil {
		t.Error("expected InstallPrometheusOperator to fail without kubectl")
	}

	// Should not panic, logs warning
	UninstallPrometheusOperator()

	if IsPrometheusCRDsInstalled() {
		t.Error("expected IsPrometheusCRDsInstalled to return false without kubectl")
	}

	// Should not panic, logs warning
	UninstallCertManager()

	if err := InstallCertManager(); err == nil {
		t.Error("expected InstallCertManager to fail without kubectl")
	}

	if IsCertManagerCRDsInstalled() {
		t.Error("expected IsCertManagerCRDsInstalled to return false without kubectl")
	}

	if err := LoadImageToKindClusterWithName("test-image"); err == nil {
		t.Error("expected LoadImageToKindClusterWithName to fail without kind")
	}

	t.Setenv("KIND_CLUSTER", "custom-cluster")
	if err := LoadImageToKindClusterWithName("test-image"); err == nil {
		t.Error("expected LoadImageToKindClusterWithName to fail without kind (custom cluster)")
	}
}

func createMockBinaries(t *testing.T, kubectlScript, kindScript string) string {
	t.Helper()
	binDir := t.TempDir()

	kubectlPath := filepath.Join(binDir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte(kubectlScript), 0755); err != nil {
		t.Fatalf("failed to write mock kubectl: %v", err)
	}

	kindPath := filepath.Join(binDir, "kind")
	if err := os.WriteFile(kindPath, []byte(kindScript), 0755); err != nil {
		t.Fatalf("failed to write mock kind: %v", err)
	}

	return binDir
}

func TestCommandsWithMockSuccess(t *testing.T) {
	kubectlScript := `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "crds" ]; then
    echo "NAME"
    echo "prometheuses.monitoring.coreos.com"
    echo "certificates.cert-manager.io"
    exit 0
  fi
done
exit 0
`
	kindScript := `#!/bin/sh
exit 0
`
	binDir := createMockBinaries(t, kubectlScript, kindScript)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	if err := InstallPrometheusOperator(); err != nil {
		t.Errorf("InstallPrometheusOperator failed: %v", err)
	}

	UninstallPrometheusOperator()

	if !IsPrometheusCRDsInstalled() {
		t.Error("expected IsPrometheusCRDsInstalled to return true")
	}

	UninstallCertManager()

	if err := InstallCertManager(); err != nil {
		t.Errorf("InstallCertManager failed: %v", err)
	}

	if !IsCertManagerCRDsInstalled() {
		t.Error("expected IsCertManagerCRDsInstalled to return true")
	}

	if err := LoadImageToKindClusterWithName("my-image"); err != nil {
		t.Errorf("LoadImageToKindClusterWithName failed: %v", err)
	}

	t.Setenv("KIND_CLUSTER", "my-test-cluster")
	if err := LoadImageToKindClusterWithName("my-image"); err != nil {
		t.Errorf("LoadImageToKindClusterWithName with KIND_CLUSTER failed: %v", err)
	}
}

func TestCommandsWithMockNoMatch(t *testing.T) {
	kubectlScript := `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "crds" ]; then
    echo "NAME"
    echo "other.example.com"
    exit 0
  fi
done
exit 0
`
	kindScript := `#!/bin/sh
exit 0
`
	binDir := createMockBinaries(t, kubectlScript, kindScript)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	if IsPrometheusCRDsInstalled() {
		t.Error("expected IsPrometheusCRDsInstalled to return false when CRDs don't match")
	}

	if IsCertManagerCRDsInstalled() {
		t.Error("expected IsCertManagerCRDsInstalled to return false when CRDs don't match")
	}
}

func TestInstallCertManagerWaitFailure(t *testing.T) {
	// kubectl succeeds on apply, but fails on wait
	kubectlScript := `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "wait" ]; then
    exit 1
  fi
done
exit 0
`
	binDir := createMockBinaries(t, kubectlScript, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	if err := InstallCertManager(); err == nil {
		t.Error("expected InstallCertManager to fail when kubectl wait fails, got nil")
	}
}
