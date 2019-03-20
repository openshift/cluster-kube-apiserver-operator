package library

import (
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"path"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

var (
	waitPollInterval = time.Second
	waitPollTimeout  = 10 * time.Minute
)

// GenerateNameForTest generates a name of the form `prefix + test name + random string` that
// can be used as a resource name. Convert the result to lowercase to use as a dns label.
func GenerateNameForTest(t *testing.T, prefix string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	FatalIfErr(t, err)
	name := []byte(fmt.Sprintf("%s%s-%016x", prefix, t.Name(), n.Int64()))
	// make the name (almost) suitable for use as a dns label
	// only a-z, 0-9, and '-' allowed
	name = regexp.MustCompile("[^a-zA-Z0-9]+").ReplaceAll(name, []byte("-"))
	// collapse multiple `-`
	name = regexp.MustCompile("-+").ReplaceAll(name, []byte("-"))
	// ensure no `-` at beginning or end
	return strings.Trim(string(name), "-")
}

// FatalIfErr marks the test as failed if there was an error.
func FatalIfErr(t *testing.T, err error) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		t.Fatalf("[error] %s:%d %v", path.Base(file), line, err)
	}
}
