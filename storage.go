package memory_ttl_storage

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"io/ioutil"
	"os"
)

type StorageManager struct {
	backupFilePath string
}

func NewStorageManager(backupFilePath string) *StorageManager {
	return &StorageManager{backupFilePath: backupFilePath}
}

func (b *StorageManager) Restore(i interface{}) error {
	restoredData, err := b.readFromFile(b.backupFilePath)
	if err != nil {
		return err
	}
	data, err := b.decompress(restoredData)
	if err != nil {
		return err
	}
	dec := gob.NewDecoder(bytes.NewReader(*data))
	err = dec.Decode(i)
	return err
}

func (b *StorageManager) Store(i interface{}) error {
	dataOut, err := b.encodeToBytes(i)
	if err != nil {
		return err
	}
	dataOut, err = b.compress(dataOut)
	if err != nil {
		return err
	}
	return b.writeToFile(dataOut, b.backupFilePath)
}

func (b *StorageManager) encodeToBytes(i interface{}) ([]byte, error) {

	buf := bytes.Buffer{}
	enc := gob.NewEncoder(&buf)
	gob.Register(i)
	err := enc.Encode(i)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (b *StorageManager) compress(s []byte) ([]byte, error) {
	zipbuf := bytes.Buffer{}
	zipped := gzip.NewWriter(&zipbuf)
	_, err := zipped.Write(s)
	zipped.Close()
	if err != nil {
		return nil, err
	}
	return zipbuf.Bytes(), nil
}

func (b *StorageManager) decompress(s []byte) (*[]byte, error) {
	rdr, _ := gzip.NewReader(bytes.NewReader(s))
	data, err := ioutil.ReadAll(rdr)
	if err != nil {
		return nil, err
	}
	rdr.Close()
	return &data, nil
}

func (b *StorageManager) writeToFile(s []byte, file string) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	f.Write(s)
	return nil
}

func (b *StorageManager) readFromFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return data, nil
}
