package goque

import (
	"os"
	"sync"

	"github.com/syndtr/goleveldb/leveldb"
)

// Stack is a standard LIFO (last in, first out) stack.
type Stack struct {
	sync.RWMutex
	DataDir string
	db      *leveldb.DB
	head    uint64
	tail    uint64
	isOpen  bool
}

// OpenStack opens a stack if one exists at the given directory. If one
// does not already exist, a new stack is created.
func OpenStack(dataDir string) (*Stack, error) {
	var err error

	// Create a new Stack.
	s := &Stack{
		DataDir: dataDir,
		db:      &leveldb.DB{},
		head:    0,
		tail:    0,
		isOpen:  false,
	}

	// Open database for the stack.
	s.db, err = leveldb.OpenFile(dataDir, nil)
	if err != nil {
		return s, err
	}

	// Check if this Goque type can open the requested data directory.
	ok, err := checkGoqueType(dataDir, goqueStack)
	if err != nil {
		return s, err
	}
	if !ok {
		return s, ErrIncompatibleType
	}

	// Set isOpen and return.
	s.isOpen = true
	return s, s.init()
}

// Push adds an item to the stack.
func (s *Stack) Push(item *Item) error {
	s.Lock()
	defer s.Unlock()

	// Set item ID and key.
	item.ID = s.head + 1
	item.Key = idToKey(item.ID)

	// Add it to the stack.
	err := s.db.Put(item.Key, item.Value, nil)
	if err == nil {
		s.head++
	}

	return err
}

// Pop removes the next item in the stack and returns it.
func (s *Stack) Pop() (*Item, error) {
	s.Lock()
	defer s.Unlock()

	// Try to get the next item in the stack.
	item, err := s.getItemByID(s.head)
	if err != nil {
		return item, err
	}

	// Remove this item from the stack.
	if err := s.db.Delete(item.Key, nil); err != nil {
		return item, err
	}

	// Decrement position.
	s.head--

	return item, nil
}

// Peek returns the next item in the stack without removing it.
func (s *Stack) Peek() (*Item, error) {
	s.RLock()
	defer s.RUnlock()
	return s.getItemByID(s.head)
}

// PeekByOffset returns the item located at the given offset,
// starting from the head of the stack, without removing it.
func (s *Stack) PeekByOffset(offset uint64) (*Item, error) {
	s.RLock()
	defer s.RUnlock()
	return s.getItemByID(s.head - offset)
}

// PeekByID returns the item with the given ID without removing it.
func (s *Stack) PeekByID(id uint64) (*Item, error) {
	s.RLock()
	defer s.RUnlock()
	return s.getItemByID(id)
}

// Update updates an item in the stack without changing its position.
func (s *Stack) Update(item *Item, newValue []byte) error {
	s.Lock()
	defer s.Unlock()
	item.Value = newValue
	return s.db.Put(item.Key, item.Value, nil)
}

// UpdateString is a helper function for Update that accepts a value
// as a string rather than a byte slice.
func (s *Stack) UpdateString(item *Item, newValue string) error {
	return s.Update(item, []byte(newValue))
}

// Length returns the total number of items in the stack.
func (s *Stack) Length() uint64 {
	return s.head - s.tail
}

// Close closes the LevelDB database of the stack.
func (s *Stack) Close() {
	// If stack is already closed.
	if !s.isOpen {
		return
	}

	s.db.Close()
	s.isOpen = false
}

// Drop closes and deletes the LevelDB database of the stack.
func (s *Stack) Drop() {
	s.Close()
	os.RemoveAll(s.DataDir)
}

// getItemByID returns an item, if found, for the given ID.
func (s *Stack) getItemByID(id uint64) (*Item, error) {
	// Check if empty or out of bounds.
	if s.Length() == 0 {
		return nil, ErrEmpty
	} else if id <= s.tail || id > s.head {
		return nil, ErrOutOfBounds
	}

	var err error
	item := &Item{ID: id, Key: idToKey(id)}
	item.Value, err = s.db.Get(item.Key, nil)

	return item, err
}

// init initializes the stack data.
func (s *Stack) init() error {
	// Create a new LevelDB Iterator.
	iter := s.db.NewIterator(nil, nil)
	defer iter.Release()

	// Set stack head to the last item.
	if iter.Last() {
		s.head = keyToID(iter.Key())
	} else {
		s.head = 0
	}

	// Set stack tail to the first item.
	if iter.First() {
		s.tail = keyToID(iter.Key()) - 1
	} else {
		s.tail = 0
	}

	return iter.Error()
}
