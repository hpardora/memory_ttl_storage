package memory_ttl_storage

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"io/ioutil"
	"os"
)

type BackupManager struct {
	backupFilePath  string
}

func NewBackupManager(backupFilePath string) *BackupManager {
	return &BackupManager{backupFilePath: backupFilePath}
}

func (b *BackupManager) Restore() (*[]byte, error){
	restoredData, err := b.readFromFile(b.backupFilePath)
	if err != nil {
		return nil, err
	}
	return b.decompress(restoredData)

}

func (b *BackupManager) Store(i interface{}) error {
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

func (b *BackupManager) encodeToBytes(p interface{}) ([]byte, error) {

	buf := bytes.Buffer{}
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(p)
	if err != nil {
		return nil, err
	}
	fmt.Println("uncompressed size (bytes): ", len(buf.Bytes()))
	return buf.Bytes(), nil
}

func (b *BackupManager) compress(s []byte) ([]byte, error) {
	zipbuf := bytes.Buffer{}
	zipped := gzip.NewWriter(&zipbuf)
	_, err := zipped.Write(s)
	zipped.Close()
	if err != nil {
		return nil, err
	}
	fmt.Println("compressed size (bytes): ", len(zipbuf.Bytes()))
	return zipbuf.Bytes(), nil
}

func (b *BackupManager) decompress(s []byte) (*[]byte, error) {
	rdr, _ := gzip.NewReader(bytes.NewReader(s))
	data, err := ioutil.ReadAll(rdr)
	if err != nil {
		return nil, err
	}
	rdr.Close()
	fmt.Println("uncompressed size (bytes): ", len(data))
	return &data, nil
}

func (b *BackupManager) writeToFile(s []byte, file string) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	f.Write(s)
	return nil
}

func (b *BackupManager) readFromFile(path string) ([]byte, error) {
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