package memory_ttl_storage

import (
	"encoding/hex"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

type MyTestStruct struct {
	A string
	B map[string]Content
}
type Content struct {
	A string
	B string
}

func TestStoreRestore(t *testing.T) {
	randBytes := make([]byte, 16)
	rand.Read(randBytes)
	filePath :=  filepath.Join(os.TempDir(), "mts_test_" + hex.EncodeToString(randBytes)+".dat")

	back := StorageManager{backupFilePath: filePath}
	c := make(map[string]Content)
	c["item"] = Content{
		A: "a",
		B: "b",
	}
	m := MyTestStruct{A: "a", B: c}

	err := back.Store(m)
	if err!=nil{
		t.Error("the store must be created", err)
	}
	result := MyTestStruct{}
	err = back.Restore(&result)
	if err != nil {
		t.Error("unable to restore data", err)
	}
	if m.A != result.A {
		t.Error("those items must be equals")
	}
}

func TestMap(t *testing.T) {
	randBytes := make([]byte, 16)
	rand.Read(randBytes)
	filePath :=  filepath.Join(os.TempDir(), "mts_test_" + hex.EncodeToString(randBytes)+".dat")

	back := StorageManager{backupFilePath: filePath}
	c := make(map[string]Content)
	c["item"] = Content{
		A: "a",
		B: "b",
	}
	m := MyTestStruct{A: "a", B: c}

	myMap := make(map[string]MyTestStruct)
	myMap["key"] = m

	err := back.Store(myMap)
	if err!=nil{
		t.Error("the store must be created", err)
	}

	result := map[string]MyTestStruct{}

	err = back.Restore(&result)
	if err != nil {
		t.Error("unable to restore data", err)
	}
	restoredItem := result["key"]

	if m.A != restoredItem.A {
		t.Error("this should be false")
	}

}
