package fileuri

import (
	"net/url"
	"path/filepath"
	"testing"
)

func TestWindowsFileReferenceKeepsAbsoluteDrivePath(t *testing.T) {
	root := fromAbsolutePath(`C:\Users\runner\contract space\root.yaml`, "windows")
	if got := root.String(); got != "file:///C:/Users/runner/contract%20space/root.yaml" {
		t.Fatalf("drive path became a URL authority: %s", got)
	}
	reference, err := url.Parse("other%20file%2520.yaml#/components/schemas/Value")
	if err != nil {
		t.Fatal(err)
	}
	resolved := root.ResolveReference(reference)
	name, err := pathForOS(resolved, "windows")
	if err != nil || name != `C:\Users\runner\contract space\other file%20.yaml` {
		t.Fatalf("resolved reference has an invalid drive prefix or double decoding: %q %v", name, err)
	}
}

func TestFileURLPathsRespectDriveAndUNCAuthorities(t *testing.T) {
	for _, test := range []struct{ uri, goos, want string }{
		{"file:///C:/contracts/root.yaml", "windows", `C:\contracts\root.yaml`},
		{"file://localhost/C:/contracts/root.yaml", "windows", `C:\contracts\root.yaml`},
		{"file://server/share/contracts/root.yaml", "windows", `\\server\share\contracts\root.yaml`},
		{"file:///tmp/contracts/root.yaml", "linux", "/tmp/contracts/root.yaml"},
		{"file://localhost/tmp/contracts/root.yaml", "linux", "/tmp/contracts/root.yaml"},
	} {
		t.Run(test.goos+" "+test.uri, func(t *testing.T) {
			uri, err := url.Parse(test.uri)
			if err != nil {
				t.Fatal(err)
			}
			got, err := pathForOS(uri, test.goos)
			if err != nil || got != test.want {
				t.Fatalf("path = %q %v, want %q", got, err, test.want)
			}
		})
	}
	unc := fromAbsolutePath(`\\server\share\contracts\root.yaml`, "windows")
	if unc.String() != "file://server/share/contracts/root.yaml" {
		t.Fatalf("UNC authority was lost: %s", unc)
	}
}

func TestNativeFileURLRoundTripAndRelativeReference(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "contracts with spaces", "root%.yaml")
	root, err := FromPath(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Path(root)
	if err != nil || got != rootPath {
		t.Fatalf("native round trip = %q %v, want %q", got, err, rootPath)
	}
	other := root.ResolveReference(&url.URL{Path: "nested/other#.yaml"})
	got, err = Path(other)
	want := filepath.Join(filepath.Dir(rootPath), "nested", "other#.yaml")
	if err != nil || got != want {
		t.Fatalf("native external reference = %q %v, want %q", got, err, want)
	}
}
