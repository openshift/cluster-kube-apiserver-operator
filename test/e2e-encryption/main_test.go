package e2e_encryption

import (
	"math/rand"
	"os"
	"reflect"
	"testing"
	"time"
	"unsafe"
)

func TestMain(m *testing.M) {
	randomizeTestOrder(m)
	os.Exit(m.Run())
}

func randomizeTestOrder(m *testing.M) {
	pointerVal := reflect.ValueOf(m)
	val := reflect.Indirect(pointerVal)

	testsMember := val.FieldByName("tests")
	ptrToTests := unsafe.Pointer(testsMember.UnsafeAddr())
	realPtrToTests := (*[]testing.InternalTest)(ptrToTests)

	tests := *realPtrToTests

	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(tests), func(i, j int) { tests[i], tests[j] = tests[j], tests[i] })

	*realPtrToTests = tests
}
