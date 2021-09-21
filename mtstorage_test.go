package memory_ttl_storage

import (
	"testing"
	"time"
)

type TestStructOne struct {
	One int
	Two string
}

type TestStructTwo struct {
	Three int
	Four string
}

func TestAddDefaultTTL(t *testing.T){
	mts := New(MemoryTTLStoreConfig{})
	ts := &TestStructOne{}
	keyTest := "test_key"
	mts.Add(keyTest, ts, nil)
	i, ok := mts.Get(keyTest)
	if !ok {
		t.Error("cannot retrieve from service")
	}
	if i.TTL != defaultTTL {
		t.Error("stored TTL is not de defaultTTL")
	}
}

func TestAddAndRetrieveAnObject(t *testing.T){
	mts := New(MemoryTTLStoreConfig{})

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
	returned := i.Content.(*TestStructOne)

	if tsOne.One != returned.One {
		t.Errorf("Unexpected respose! Expected %d, got %d", tsOne.One, returned.One)
	}
}

func TestElementDontExistsAfterTTL(t *testing.T){
	mts := New(MemoryTTLStoreConfig{TTLValue: 1})
	ts := &TestStructOne{}
	keyTest := "test_key"
	mts.Add(keyTest, ts, nil)
	time.Sleep(time.Second * 3)
	_, ok := mts.Get(keyTest)
	if ok {
		t.Error("you should not be able to see retrieve this element")
	}

}