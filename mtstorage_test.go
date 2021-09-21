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
	mts := New(nil)
	defer mts.Stop()

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
	returned := i.Content.(*TestStructOne)

	if tsOne.One != returned.One {
		t.Errorf("Unexpected respose! Expected %d, got %d", tsOne.One, returned.One)
	}
}

func TestElementDontExistsAfterTTL(t *testing.T){
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
	defer mts.Stop()
}

func TestRefreshTTL(t *testing.T){
	mts := New(nil)
	defer mts.Stop()

	ts := &TestStructOne{}
	keyTest := "test_key"
	mts.Add(keyTest, ts, nil)
	time.Sleep(time.Second * 1)

	tempItem, ok := mts.GetAndRefresh(keyTest)
	if !ok {
		t.Error("the element has to be restored")
	}
	time.Sleep(time.Second * 1)
	finalItem, ok := mts.Get(keyTest)
	if !ok {
		t.Error("the element has to be restored")
	}
	if tempItem.Content != finalItem.Content {
		t.Error("content at this point must be equal")
	}
	if tempItem.ExpireTimestamp >= finalItem.ExpireTimestamp {
		t.Error("exp time of initial element must be lower than the element after get and refresh")
	}
}

func TestGetDontModifyExpTS(t *testing.T){
	mts := New(nil)
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
	if tempItem.Content != finalItem.Content {
		t.Error("content at this point must be equal")
	}
	if tempItem.ExpireTimestamp != finalItem.ExpireTimestamp {
		t.Error("exp time of initial element must be equal to the exp time of the second recovered element")
	}
}