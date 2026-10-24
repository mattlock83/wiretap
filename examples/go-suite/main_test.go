package orders

import (
	"os"
	"testing"

	wiretap "github.com/mattlock83/wiretap/sdk/go"
)

func TestMain(m *testing.M) {
	os.Exit(wiretap.Run(m, wiretap.RunOptions{Handler: NewHandler()}))
}
