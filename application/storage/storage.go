package gatesentry2storage

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
)

const ENCRYPTIONKEY = "AES256Key-A23AS98BVM94PO3XSD10AA"

var GSBASEDIR = "./gatesentry"

type Store struct {
	Id            string
	Encrypted     bool
	Encryptionkey string
	Data          []byte
}

func SetBaseDir(dir string) {
	GSBASEDIR = dir
}

func NewStore(name string, encrypt bool) *Store {
	s := &Store{Id: name, Encrypted: encrypt, Encryptionkey: ENCRYPTIONKEY}
	s.Load()
	s.Persist()
	return s
}

func (s *Store) Load() {
	data, err := os.ReadFile(GSBASEDIR + s.Id)
	if err != nil {
		fmt.Println("Unable to load Settings file: " + s.Id + " . Will create a new one.")
		s.Data = []byte{}
		return
	}
	mm := make(map[string]string)
	if len(data) > 0 {
		if err := json.Unmarshal(data, &mm); err != nil {
			fmt.Printf("Storage Error in MapStore Load: %s\n", err)
			s.Data = []byte{}
			return
		}
	}
	if mm["encrypted"] == "true" {
		data, err = Decrypt([]byte(mm["data"]), []byte(s.Encryptionkey))
		if err != nil {
			fmt.Println("Storage Error in MapStore Decrypt : " + err.Error())
			data = []byte{}
		}
		s.Data = data
	} else {
		s.Data = []byte(mm["data"])
	}
}

func (s *Store) Persist() {
	mm := make(map[string]string)
	mm["data"] = string(s.Data)
	if s.Encrypted {
		mm["encrypted"] = "true"
		encryptedData, err := Encrypt(s.Data, []byte(s.Encryptionkey))
		if err != nil {
			fmt.Println(err.Error())
			encryptedData = []byte{}
		}
		mm["data"] = string(encryptedData)
	} else {
		mm["encrypted"] = "false"
	}
	mapJson, _ := json.Marshal(mm)

	b := []byte(string(mapJson))
	os.WriteFile(GSBASEDIR+s.Id, b, 0644)
}

func (s *Store) Set(data []byte) {
	s.Data = data
	s.Persist()
}

func (s *Store) Get() []byte {
	return s.Data
}

type MapStore struct {
	BaseStore *Store
	mu        sync.RWMutex
	data      map[string]string
}

func NewMapStore(name string, encrypt bool) *MapStore {
	s := &Store{Id: name, Encrypted: encrypt, Encryptionkey: ENCRYPTIONKEY}
	m := &MapStore{BaseStore: s, data: make(map[string]string)}
	s.Load()
	m.data = parseMap(s.Data)
	return m
}

func parseMap(data []byte) map[string]string {
	mm := make(map[string]string)
	if len(data) == 0 {
		return mm
	}
	if err := json.Unmarshal(data, &mm); err != nil {
		fmt.Printf("Storage Error parsing MapStore: %s\n", err)
		return make(map[string]string)
	}
	return mm
}

func (m *MapStore) persistLocked() {
	mapJson, err := json.Marshal(m.data)
	if err != nil {
		fmt.Printf("Storage Error marshaling MapStore: %s\n", err)
		return
	}
	m.BaseStore.Set(mapJson)
}

// Reload re-reads the store from disk into the existing MapStore.
// Callers that already hold a pointer (webserver, DNS) keep using it.
func (m *MapStore) Reload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.BaseStore.Load()
	m.data = parseMap(m.BaseStore.Get())
}

func (m *MapStore) Update(key string, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = make(map[string]string)
	}
	m.data[key] = value
	m.persistLocked()
}

func (m *MapStore) Get(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.data == nil {
		return ""
	}
	return m.data[key]
}

func (m *MapStore) GetInt(key string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.data == nil {
		return 0
	}
	i, err := strconv.Atoi(m.data[key])
	if err != nil {
		return 0
	}
	return i
}

func (m *MapStore) SetDefault(key string, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = make(map[string]string)
	}
	if _, ok := m.data[key]; ok {
		return
	}
	m.data[key] = value
	m.persistLocked()
}
