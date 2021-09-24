package memory_ttl_storage

import (
	"encoding/gob"
	"os"
	"testing"
	"time"
)

type TestStructOne struct {
	One int
	Two string
}

type TestStructTwo struct {
	Three int
	Four  string
}

const testPath = "/tmp/the storage_test.dat"
const testKey = "test_key"

func TestAddDefaultTTL(t *testing.T) {
	mts := New(nil)
	defer mts.Stop()

	ts := &TestStructOne{}
	keyTest := "test_key"
	mts.Add(keyTest, ts, nil)
	_, ok := mts.Get(keyTest)
	if !ok {
		t.Error("cannot retrieve from service")
	}

}

func TestAddAndRetrieveAnObject(t *testing.T) {
	mts := New(nil)
	defer mts.Stop()

	keyTestOne := "test_key_one"
	tsOne := &TestStructOne{One: 1, Two: "two"}

	keyTestTwo := "test_key_two"
	tsTwo := &TestStructTwo{Three: 3, Four: "four"}

	mts.Add(keyTestOne, tsOne, nil)
	mts.Add(keyTestTwo, tsTwo, nil)

	i, ok := mts.Get(keyTestOne)
	if !ok {
		t.Error("cannot retrieve from service")
	}
	returned := i.(*TestStructOne)

	if tsOne.One != returned.One {
		t.Errorf("Unexpected respose! Expected %d, got %d", tsOne.One, returned.One)
	}
}

func TestElementDontExistsAfterTTL(t *testing.T) {
	mts := New(&MemoryTTLStoreConfig{TTLValue: 1})
	defer mts.Stop()

	ts := &TestStructOne{}
	keyTest := "test_key"
	mts.Add(keyTest, ts, nil)
	time.Sleep(time.Second * 3)
	_, ok := mts.Get(keyTest)
	if ok {
		t.Error("you should not be able to see retrieve this element")
	}
}

func TestRefreshTTL(t *testing.T) {
	mts := New(&MemoryTTLStoreConfig{TTLValue: 3})
	defer mts.Stop()
	ts := &TestStructOne{}

	keyTest := "test_key"
	mts.Add(keyTest, ts, nil)
	time.Sleep(time.Second * 2)

	tempItem, ok := mts.GetAndRefresh(keyTest)
	if !ok {
		t.Error("the element has to be restored")
	}
	time.Sleep(time.Second * 2)
	finalItem, ok := mts.Get(keyTest)
	if !ok {
		t.Error("the element has to be restored")
	}
	if tempItem != finalItem {
		t.Error("content at this point must be equal")
	}

}

func TestGetDontModifyExpTS(t *testing.T) {
	mts := New(&MemoryTTLStoreConfig{TTLValue: 2})
	defer mts.Stop()

	ts := &TestStructOne{}
	keyTest := "test_key"
	mts.Add(keyTest, ts, nil)
	time.Sleep(time.Second * 1)

	tempItem, ok := mts.Get(keyTest)
	if !ok {
		t.Error("the element has to be restored")
	}

	time.Sleep(time.Second * 1)
	finalItem, ok := mts.Get(keyTest)
	if !ok {
		t.Error("the element has to be restored")
	}
	if tempItem != finalItem {
		t.Error("content at this point must be equal")
	}

	time.Sleep(time.Second * 2)
	_, ok = mts.Get(keyTest)
	if ok {
		t.Error("item should not exist")
	}
}

func TestFileStore(t *testing.T) {
	mts := New(&MemoryTTLStoreConfig{TTLValue: 2, BackupPath: testPath, UseBackup: true})
	gob.Register(TestStructOne{})
	test := TestStructOne{One: 1, Two: "two"}
	mts.Add(testKey, test, nil)
	mts.Stop()
	os.Remove(testPath)
}

func TestFileStoreRestore(t *testing.T) {
	mts := New(&MemoryTTLStoreConfig{TTLValue: 2, BackupPath: testPath, UseBackup: true})
	gob.Register(TestStructOne{})
	test := TestStructOne{One: 1, Two: "two"}
	mts.Add(testKey, test, nil)
	mts.Stop()

	mts2 := New(&MemoryTTLStoreConfig{TTLValue: 2, BackupPath: testPath, UseBackup: true})
	tmp, ok := mts2.Get(testKey)
	if !ok {
		t.Error("You must retrieve the initial item at this point")
	}
	test2 := tmp.(TestStructOne)
	if test != test2 {
		t.Error("at this moment the items must be equals")
	}
	os.Remove(testPath)

}
