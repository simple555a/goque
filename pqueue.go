package goque

import (
	"os"
	"sync"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// prefixSep is the prefix separator for each item key.
var prefixSep []byte = []byte(":")

// order defines the priority ordering of the queue.
type order int

// Defines which priority order to dequeue in.
const (
	ASC  order = iota // Set priority level 0 as most important.
	DESC              // Set priority level 255 as most important.
)

// priorityLevel holds the head and tail position of a priority
// level within the queue.
type priorityLevel struct {
	head uint64
	tail uint64
}

// length returns the total number of items in this priority level.
func (pl *priorityLevel) length() uint64 {
	return pl.tail - pl.head
}

// PriorityQueue is a standard FIFO (first in, first out) queue with
// priority levels.
type PriorityQueue struct {
	sync.RWMutex
	DataDir  string
	db       *leveldb.DB
	order    order
	levels   [256]*priorityLevel
	curLevel uint8
	isOpen   bool
}

// OpenPriorityQueue opens a priority queue if one exists at the given
// directory. If one does not already exist, a new priority queue is
// created.
func OpenPriorityQueue(dataDir string, order order) (*PriorityQueue, error) {
	var err error

	// Create a new PriorityQueue.
	pq := &PriorityQueue{
		DataDir: dataDir,
		db:      &leveldb.DB{},
		order:   order,
		isOpen:  false,
	}

	// Open database for the priority queue.
	pq.db, err = leveldb.OpenFile(dataDir, nil)
	if err != nil {
		return pq, err
	}

	// Check if this Goque type can open the requested data directory.
	ok, err := checkGoqueType(dataDir, goquePriorityQueue)
	if err != nil {
		return pq, err
	}
	if !ok {
		return pq, ErrIncompatibleType
	}

	// Set isOpen and return.
	pq.isOpen = true
	return pq, pq.init()
}

// Enqueue adds an item to the priority queue.
func (pq *PriorityQueue) Enqueue(item *PriorityItem) error {
	pq.Lock()
	defer pq.Unlock()

	// Get the priorityLevel.
	level := pq.levels[item.Priority]

	// Set item ID and key.
	item.ID = level.tail + 1
	item.Key = pq.generateKey(item.Priority, item.ID)

	// Add it to the priority queue.
	err := pq.db.Put(item.Key, item.Value, nil)
	if err == nil {
		level.tail++

		// If this priority level is more important than the curLevel.
		if pq.cmpAsc(item.Priority) || pq.cmpDesc(item.Priority) {
			pq.curLevel = item.Priority
		}
	}

	return err
}

// Dequeue removes the next item in the priority queue and returns it.
func (pq *PriorityQueue) Dequeue() (*PriorityItem, error) {
	pq.Lock()
	defer pq.Unlock()

	// Try to get the next item in the current priority level.
	item, err := pq.getNextItem()
	if err != nil {
		return item, err
	}

	// Remove this item from the priority queue.
	if err = pq.db.Delete(item.Key, nil); err != nil {
		return item, err
	}

	// Increment position.
	pq.levels[pq.curLevel].head++

	return item, nil
}

// DequeueByPriority removes the next item in the given priority level
// and returns it.
func (pq *PriorityQueue) DequeueByPriority(priority uint8) (*PriorityItem, error) {
	pq.Lock()
	defer pq.Unlock()

	// Try to get the next item in the given priority level.
	item, err := pq.getItemByPriorityID(priority, pq.levels[priority].head+1)
	if err != nil {
		return item, err
	}

	// Remove this item from the priority queue.
	if err = pq.db.Delete(item.Key, nil); err != nil {
		return item, err
	}

	// Increment position.
	pq.levels[priority].head++

	return item, nil
}

// Peek returns the next item in the priority queue without removing it.
func (pq *PriorityQueue) Peek() (*PriorityItem, error) {
	pq.RLock()
	defer pq.RUnlock()
	return pq.getNextItem()
}

// PeekByOffset returns the item located at the given offset,
// starting from the head of the queue, without removing it.
func (pq *PriorityQueue) PeekByOffset(offset uint64) (*PriorityItem, error) {
	pq.RLock()
	defer pq.RUnlock()

	// If the offset is within the current priority level.
	if pq.levels[pq.curLevel].length()-1 >= offset {
		return pq.getItemByPriorityID(pq.curLevel, pq.levels[pq.curLevel].head+offset+1)
	}

	if pq.order == ASC {
		return pq.findOffsetAsc(offset)
	} else if pq.order == DESC {
		return pq.findOffsetDesc(offset)
	}

	return nil, nil
}

// PeekByPriorityID returns the item with the given ID and priority without
// removing it.
func (pq *PriorityQueue) PeekByPriorityID(priority uint8, id uint64) (*PriorityItem, error) {
	pq.RLock()
	defer pq.RUnlock()
	return pq.getItemByPriorityID(priority, id)
}

// Update updates an item in the priority queue without changing its
// position.
func (pq *PriorityQueue) Update(item *PriorityItem, newValue []byte) error {
	pq.Lock()
	defer pq.Unlock()
	item.Value = newValue
	return pq.db.Put(item.Key, item.Value, nil)
}

// UpdateString is a helper function for Update that accepts a value
// as a string rather than a byte slice.
func (pq *PriorityQueue) UpdateString(item *PriorityItem, newValue string) error {
	return pq.Update(item, []byte(newValue))
}

// Length returns the total number of items in the priority queue.
func (pq *PriorityQueue) Length() uint64 {
	var length uint64
	for _, v := range pq.levels {
		length += v.length()
	}

	return length
}

// Close closes the LevelDB database of the priority queue.
func (pq *PriorityQueue) Close() {
	// If queue is already closed.
	if !pq.isOpen {
		return
	}

	pq.db.Close()
	pq.isOpen = false
}

// Drop closes and deletes the LevelDB database of the priority queue.
func (pq *PriorityQueue) Drop() {
	pq.Close()
	os.RemoveAll(pq.DataDir)
}

// cmpAsc returns wehther the given priority level is higher than the
// current priority level based on ascending order.
func (pq *PriorityQueue) cmpAsc(priority uint8) bool {
	return pq.order == ASC && priority < pq.curLevel
}

// cmpAsc returns wehther the given priority level is higher than the
// current priority level based on descending order.
func (pq *PriorityQueue) cmpDesc(priority uint8) bool {
	return pq.order == DESC && priority > pq.curLevel
}

// resetCurrentLevel resets the current priority level of the queue
// so the highest level can be found.
func (pq *PriorityQueue) resetCurrentLevel() {
	if pq.order == ASC {
		pq.curLevel = 255
	} else if pq.order == DESC {
		pq.curLevel = 0
	}
}

// findOffsetAsc finds the given offset from the current queue
// position based on ascending order.
func (pq *PriorityQueue) findOffsetAsc(offset uint64) (*PriorityItem, error) {
	var length uint64
	var priority uint8 = pq.curLevel

	// Loop through the priority levels.
	for i := 0; i <= 255; i++ {
		iu8 := uint8(i)

		// If this level is lower than the current level based on ordering and contains items.
		if iu8 >= priority && pq.levels[iu8].length() > 0 {
			priority = iu8
			newLength := pq.levels[iu8].length() - 1

			// If the offset is within the current priority level.
			if length+newLength >= offset {
				return pq.getItemByPriorityID(priority, offset-length+1)
			}

			length += newLength + 1
		}
	}

	return nil, ErrOutOfBounds
}

// findOffsetDesc finds the given offset from the current queue
// position based on descending order.
func (pq *PriorityQueue) findOffsetDesc(offset uint64) (*PriorityItem, error) {
	var length uint64
	var priority uint8 = pq.curLevel

	// Loop through the priority levels.
	for i := 255; i >= 0; i-- {
		iu8 := uint8(i)

		// If this level is lower than the current level based on ordering and contains items.
		if iu8 <= priority && pq.levels[iu8].length() > 0 {
			priority = iu8
			newLength := pq.levels[iu8].length() - 1

			// If the offset is within the current priority level.
			if length+newLength >= offset {
				return pq.getItemByPriorityID(priority, offset-length+1)
			}

			length += newLength + 1
		}
	}

	return nil, ErrOutOfBounds
}

// getNextItem returns the next item in the priority queue, updating
// the current priority level of the queue if necessary.
func (pq *PriorityQueue) getNextItem() (*PriorityItem, error) {
	// If the current priority level is empty.
	if pq.levels[pq.curLevel].length() == 0 {
		// Set starting value for curLevel.
		pq.resetCurrentLevel()

		// Try to get the next priority level.
		for i := 0; i <= 255; i++ {
			if (pq.cmpAsc(uint8(i)) || pq.cmpDesc(uint8(i))) && pq.levels[uint8(i)].length() > 0 {
				pq.curLevel = uint8(i)
			}
		}

		// If still empty, return queue empty error.
		if pq.levels[pq.curLevel].length() == 0 {
			return nil, ErrEmpty
		}
	}

	// Try to get the next item in the current priority level.
	return pq.getItemByPriorityID(pq.curLevel, pq.levels[pq.curLevel].head+1)
}

// getItemByID returns an item, if found, for the given ID.
func (pq *PriorityQueue) getItemByPriorityID(priority uint8, id uint64) (*PriorityItem, error) {
	// Check if empty or out of bounds.
	if pq.levels[priority].length() == 0 {
		return nil, ErrEmpty
	} else if id <= pq.levels[priority].head || id > pq.levels[priority].tail {
		return nil, ErrOutOfBounds
	}

	var err error

	// Create a new PriorityItem.
	item := &PriorityItem{ID: id, Priority: priority, Key: pq.generateKey(priority, id)}
	item.Value, err = pq.db.Get(item.Key, nil)

	return item, err
}

// generatePrefix creates the key prefix for the given priority level.
func (pq *PriorityQueue) generatePrefix(level uint8) []byte {
	// priority + prefixSep = 1 + 1 = 2
	prefix := make([]byte, 2)
	prefix[0] = byte(level)
	prefix[1] = prefixSep[0]
	return prefix
}

// generateKey create a key to be used with LevelDB.
func (pq *PriorityQueue) generateKey(priority uint8, id uint64) []byte {
	// prefix + key = 2 + 8 = 10
	key := make([]byte, 10)
	copy(key[0:2], pq.generatePrefix(priority))
	copy(key[2:], idToKey(id))
	return key
}

// init initializes the priority queue data.
func (pq *PriorityQueue) init() error {
	// Set starting value for curLevel.
	pq.resetCurrentLevel()

	// Loop through each priority level.
	for i := 0; i <= 255; i++ {
		// Create a new LevelDB Iterator for this priority level.
		prefix := pq.generatePrefix(uint8(i))
		iter := pq.db.NewIterator(util.BytesPrefix(prefix), nil)

		// Create a new priorityLevel.
		pl := &priorityLevel{
			head: 0,
			tail: 0,
		}

		// Set priority level head to the first item.
		if iter.First() {
			pl.head = keyToID(iter.Key()[2:]) - 1

			// Since this priority level has item(s), handle updating curLevel.
			if pq.cmpAsc(uint8(i)) || pq.cmpDesc(uint8(i)) {
				pq.curLevel = uint8(i)
			}
		}

		// Set priority level tail to the last item.
		if iter.Last() {
			pl.tail = keyToID(iter.Key()[2:])
		}

		if iter.Error() != nil {
			return iter.Error()
		}

		pq.levels[i] = pl
		iter.Release()
	}

	return nil
}
