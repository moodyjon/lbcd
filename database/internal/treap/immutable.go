// Copyright (c) 2015-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package treap

import (
	"bytes"
	"math/rand"
	"sync"
)

// cloneTreapNode returns a shallow copy of the passed node.
func cloneTreapNode(node *treapNode) *treapNode {
	clone := getTreapNode(node.key, node.value, node.priority, node.generation+1)
	clone.left = node.left
	clone.right = node.right
	return clone
}

// Immutable represents a treap data structure which is used to hold ordered
// key/value pairs using a combination of binary search tree and heap semantics.
// It is a self-organizing and randomized data structure that doesn't require
// complex operations to maintain balance.  Search, insert, and delete
// operations are all O(log n).  In addition, it provides O(1) snapshots for
// multi-version concurrency control (MVCC).
//
// All operations which result in modifying the treap return a new version of
// the treap with only the modified nodes updated.  All unmodified nodes are
// shared with the previous version.  This is extremely useful in concurrent
// applications since the caller only has to atomically replace the treap
// pointer with the newly returned version after performing any mutations.  All
// readers can simply use their existing pointer as a snapshot since the treap
// it points to is immutable.  This effectively provides O(1) snapshot
// capability with efficient memory usage characteristics since the old nodes
// only remain allocated until there are no longer any references to them.
type Immutable struct {
	root  *treapNode
	count int

	// totalSize is the best estimate of the total size of of all data in
	// the treap including the keys, values, and node sizes.
	totalSize uint64

	// generation number starts at 0 after NewImmutable(), and
	// is incremented with every Put()/Delete().
	generation int

	// snap is a pointer to a node in snapshot history linked list.
	// A value nil means no snapshots are outstanding.
	snap **SnapRecord
}

// newImmutable returns a new immutable treap given the passed parameters.
func newImmutable(root *treapNode, count int, totalSize uint64, generation int, snap **SnapRecord) *Immutable {
	return &Immutable{root: root, count: count, totalSize: totalSize, generation: generation, snap: snap}
}

// Len returns the number of items stored in the treap.
func (t *Immutable) Len() int {
	return t.count
}

// Size returns a best estimate of the total number of bytes the treap is
// consuming including all of the fields used to represent the nodes as well as
// the size of the keys and values.  Shared values are not detected, so the
// returned size assumes each value is pointing to different memory.
func (t *Immutable) Size() uint64 {
	return t.totalSize
}

// get returns the treap node that contains the passed key.  It will return nil
// when the key does not exist.
func (t *Immutable) get(key []byte) *treapNode {
	for node := t.root; node != nil; {
		// Traverse left or right depending on the result of the
		// comparison.
		compareResult := bytes.Compare(key, node.key)
		if compareResult < 0 {
			node = node.left
			continue
		}
		if compareResult > 0 {
			node = node.right
			continue
		}

		// The key exists.
		return node
	}

	// A nil node was reached which means the key does not exist.
	return nil
}

// Has returns whether or not the passed key exists.
func (t *Immutable) Has(key []byte) bool {
	if node := t.get(key); node != nil {
		return true
	}
	return false
}

// Get returns the value for the passed key.  The function will return nil when
// the key does not exist.
func (t *Immutable) Get(key []byte) []byte {
	if node := t.get(key); node != nil {
		return node.value
	}
	return nil
}

// put inserts the passed key/value pair.
func (t *Immutable) put(key, value []byte) (tp *Immutable, old parentStack) {
	// Use an empty byte slice for the value when none was provided.  This
	// ultimately allows key existence to be determined from the value since
	// an empty byte slice is distinguishable from nil.
	if value == nil {
		value = emptySlice
	}

	// The node is the root of the tree if there isn't already one.
	if t.root == nil {
		root := getTreapNode(key, value, rand.Int(), t.generation+1)
		return newImmutable(root, 1, nodeSize(root), t.generation+1, t.snap), parentStack{}
	}

	// Find the binary tree insertion point and construct a replaced list of
	// parents while doing so.  This is done because this is an immutable
	// data structure so regardless of where in the treap the new key/value
	// pair ends up, all ancestors up to and including the root need to be
	// replaced.
	//
	// When the key matches an entry already in the treap, replace the node
	// with a new one that has the new value set and return.
	var parents parentStack
	var oldParents parentStack
	var compareResult int
	for node := t.root; node != nil; {
		// Clone the node and link its parent to it if needed.
		oldParents.Push(node)
		nodeCopy := cloneTreapNode(node)
		if oldParent := parents.At(0); oldParent != nil {
			if oldParent.left == node {
				oldParent.left = nodeCopy
			} else {
				oldParent.right = nodeCopy
			}
		}
		parents.Push(nodeCopy)

		// Traverse left or right depending on the result of comparing
		// the keys.
		compareResult = bytes.Compare(key, node.key)
		if compareResult < 0 {
			node = node.left
			continue
		}
		if compareResult > 0 {
			node = node.right
			continue
		}

		// The key already exists, so update its value.
		nodeCopy.value = value

		// Return new immutable treap with the replaced node and
		// ancestors up to and including the root of the tree.
		newRoot := parents.At(parents.Len() - 1)
		newTotalSize := t.totalSize - uint64(len(node.value)) +
			uint64(len(value))
		return newImmutable(newRoot, t.count, newTotalSize, t.generation+1, t.snap), oldParents
	}

	// Link the new node into the binary tree in the correct position.
	node := getTreapNode(key, value, rand.Int(), t.generation+1)
	parent := parents.At(0)
	if compareResult < 0 {
		parent.left = node
	} else {
		parent.right = node
	}

	// Perform any rotations needed to maintain the min-heap and replace
	// the ancestors up to and including the tree root.
	newRoot := parents.At(parents.Len() - 1)
	for parents.Len() > 0 {
		// There is nothing left to do when the node's priority is
		// greater than or equal to its parent's priority.
		parent = parents.Pop()
		if node.priority >= parent.priority {
			break
		}

		// Perform a right rotation if the node is on the left side or
		// a left rotation if the node is on the right side.
		if parent.left == node {
			node.right, parent.left = parent, node.right
		} else {
			node.left, parent.right = parent, node.left
		}

		// Either set the new root of the tree when there is no
		// grandparent or relink the grandparent to the node based on
		// which side the old parent the node is replacing was on.
		grandparent := parents.At(0)
		if grandparent == nil {
			newRoot = node
		} else if grandparent.left == parent {
			grandparent.left = node
		} else {
			grandparent.right = node
		}
	}

	return newImmutable(newRoot, t.count+1, t.totalSize+nodeSize(node), t.generation+1, t.snap), oldParents
}

// Put is the immutable variant of put. Old nodes become garbage unless referenced elswhere.
func (t *Immutable) Put(key, value []byte) *Immutable {
	tp, _ := t.put(key, value)
	return tp
}

// PutM is the mutable variant of put. Old nodes are recycled if possible. This is
// only safe in structured scenarios using SnapRecord to track treap instances.
// The outstanding SnapRecords serve to protect nodes from recycling when they might
// be present in one or more snapshots. This is useful in scenarios where multiple
// Put/Delete() ops are applied to a treap and intermediate treap states are not
// created or desired. For example:
//
//     for i := range keys {
//         t = t.Put(keys[i])
//     }
//
// ...may be replaced with:
//
//     for i := range keys {
//         PutM(t, keys[i], nil)
//     }
//
// If "excluded" is provided, that snapshot is ignored when counting
// snapshot records.
//
func PutM(dest **Immutable, key, value []byte, excluded *SnapRecord) {
	tp, old := (*dest).put(key, value)
	// Examine old nodes and recycle if possible.
	snapRecordMutex.Lock()
	defer snapRecordMutex.Unlock()
	snapCount, maxSnap, minSnap := (*dest).snapCount(nil)
	for old.Len() > 0 {
		node := old.Pop()
		if snapCount == 0 || node.generation > maxSnap.generation {
			putTreapNode(node)
		} else {
			// Defer recycle until Release() on oldest snap (minSnap).
			node.generation = recycleGeneration
			node.next = minSnap.recycle
			minSnap.recycle = node
		}
	}
	*dest = tp
}

// del removes the passed key from the treap and returns the resulting treap
// if it exists.  The original immutable treap is returned if the key does not
// exist.
func (t *Immutable) del(key []byte) (d *Immutable, old parentStack) {
	// Find the node for the key while constructing a list of parents while
	// doing so.
	var oldParents parentStack
	var delNode *treapNode
	for node := t.root; node != nil; {
		oldParents.Push(node)

		// Traverse left or right depending on the result of the
		// comparison.
		compareResult := bytes.Compare(key, node.key)
		if compareResult < 0 {
			node = node.left
			continue
		}
		if compareResult > 0 {
			node = node.right
			continue
		}

		// The key exists.
		delNode = node
		break
	}

	// There is nothing to do if the key does not exist.
	if delNode == nil {
		return t, parentStack{}
	}

	// When the only node in the tree is the root node and it is the one
	// being deleted, there is nothing else to do besides removing it.
	parent := oldParents.At(1)
	if parent == nil && delNode.left == nil && delNode.right == nil {
		return newImmutable(nil, 0, 0, t.generation+1, t.snap), oldParents
	}

	// Construct a replaced list of parents and the node to delete itself.
	// This is done because this is an immutable data structure and
	// therefore all ancestors of the node that will be deleted, up to and
	// including the root, need to be replaced.
	var newParents parentStack
	for i := oldParents.Len(); i > 0; i-- {
		node := oldParents.At(i - 1)
		nodeCopy := cloneTreapNode(node)
		if oldParent := newParents.At(0); oldParent != nil {
			if oldParent.left == node {
				oldParent.left = nodeCopy
			} else {
				oldParent.right = nodeCopy
			}
		}
		newParents.Push(nodeCopy)
	}
	delNode = newParents.Pop()
	parent = newParents.At(0)

	// Perform rotations to move the node to delete to a leaf position while
	// maintaining the min-heap while replacing the modified children.
	var child *treapNode
	newRoot := newParents.At(newParents.Len() - 1)
	for delNode.left != nil || delNode.right != nil {
		// Choose the child with the higher priority.
		var isLeft bool
		if delNode.left == nil {
			child = delNode.right
		} else if delNode.right == nil {
			child = delNode.left
			isLeft = true
		} else if delNode.left.priority >= delNode.right.priority {
			child = delNode.left
			isLeft = true
		} else {
			child = delNode.right
		}

		// Rotate left or right depending on which side the child node
		// is on.  This has the effect of moving the node to delete
		// towards the bottom of the tree while maintaining the
		// min-heap.
		child = cloneTreapNode(child)
		if isLeft {
			child.right, delNode.left = delNode, child.right
		} else {
			child.left, delNode.right = delNode, child.left
		}

		// Either set the new root of the tree when there is no
		// grandparent or relink the grandparent to the node based on
		// which side the old parent the node is replacing was on.
		//
		// Since the node to be deleted was just moved down a level, the
		// new grandparent is now the current parent and the new parent
		// is the current child.
		if parent == nil {
			newRoot = child
		} else if parent.left == delNode {
			parent.left = child
		} else {
			parent.right = child
		}

		// The parent for the node to delete is now what was previously
		// its child.
		parent = child
	}

	// Delete the node, which is now a leaf node, by disconnecting it from
	// its parent.
	if parent.right == delNode {
		parent.right = nil
	} else {
		parent.left = nil
	}

	return newImmutable(newRoot, t.count-1, t.totalSize-nodeSize(delNode), t.generation+1, t.snap), oldParents
}

// Delete is the immutable variant of del. Old nodes become garbage unless referenced elswhere.
func (t *Immutable) Delete(key []byte) *Immutable {
	tp, _ := t.del(key)
	return tp
}

// DeleteM is the mutable variant of del. Old nodes are recycled if possible. This is
// only safe in structured scenarios using SnapRecord to track treap instances.
// The outstanding SnapRecords serve to protect nodes from recycling when they might
// be present in one or more snapshots. This is useful in scenarios where multiple
// Put/Delete() ops are applied to a treap and intermediate treap states are not
// created or desired. For example:
//
//     for i := range keys {
//         t = t.Delete(keys[i])
//     }
//
// ...may be replaced with:
//
//     for i := range keys {
//         DeleteM(t, keys[i], nil)
//     }
//
// If "excluded" is provided, that snapshot is ignored when counting
// snapshot records.
//
func DeleteM(dest **Immutable, key []byte, excluded *SnapRecord) {
	tp, old := (*dest).del(key)
	// Examine old nodes and recycle if possible.
	snapRecordMutex.Lock()
	defer snapRecordMutex.Unlock()
	snapCount, maxSnap, minSnap := (*dest).snapCount(nil)
	for old.Len() > 0 {
		node := old.Pop()
		if snapCount == 0 || node.generation > maxSnap.generation {
			putTreapNode(node)
		} else {
			// Defer recycle until Release() on oldest snap (minSnap).
			node.generation = recycleGeneration
			node.next = minSnap.recycle
			minSnap.recycle = node
		}
	}
	*dest = tp
}

// ForEach invokes the passed function with every key/value pair in the treap
// in ascending order.
func (t *Immutable) ForEach(fn func(k, v []byte) bool) {
	// Add the root node and all children to the left of it to the list of
	// nodes to traverse and loop until they, and all of their child nodes,
	// have been traversed.
	var parents parentStack
	for node := t.root; node != nil; node = node.left {
		parents.Push(node)
	}
	for parents.Len() > 0 {
		node := parents.Pop()
		if !fn(node.key, node.value) {
			return
		}

		// Extend the nodes to traverse by all children to the left of
		// the current node's right child.
		for node := node.right; node != nil; node = node.left {
			parents.Push(node)
		}
	}
}

// NewImmutable returns a new empty immutable treap ready for use.  See the
// documentation for the Immutable structure for more details.
func NewImmutable() *Immutable {
	return &Immutable{}
}

// SnapRecord assists in tracking outstanding snapshots. While a SnapRecord
// is present and has not been Released(), treap nodes at or below this
// generation are protected from Recycle().
type SnapRecord struct {
	generation int
	rp         **SnapRecord
	prev       *SnapRecord
	next       *SnapRecord
	recycle    *treapNode
}

var snapRecordMutex sync.Mutex

// Snapshot makes a SnapRecord and links it into the snapshot history of a treap.
func (t *Immutable) Snapshot() *SnapRecord {
	snapRecordMutex.Lock()
	defer snapRecordMutex.Unlock()

	rp := t.snap
	var next *SnapRecord = nil
	var prev *SnapRecord = nil
	if rp != nil {
		prev = *rp
		if *rp != nil {
			next = (*rp).next
		}
	}

	// Create a new record stamped with the current generation. Link it
	// following the existing snapshot record, if any.
	p := new(*SnapRecord)
	*p = &SnapRecord{generation: t.generation, rp: p, prev: prev, next: next}
	t.snap = p

	if rp != nil && *rp != nil {
		(*rp).next = *(t.snap)
	}

	return *(t.snap)
}

// Release of SnapRecord unlinks that record from the snapshot history of a treap.
func (r *SnapRecord) Release() {
	snapRecordMutex.Lock()
	defer snapRecordMutex.Unlock()

	// Unlink this record.
	*(r.rp) = nil
	if r.next != nil {
		r.next.prev = r.prev
		*(r.rp) = r.next
	}
	if r.prev != nil {
		r.prev.next = r.next
		*(r.rp) = r.prev
	}

	// Handle deferred recycle list.
	for node := r.recycle; node != nil; {
		next := node.next
		putTreapNode(node)
		node = next
	}
}

// snapCount returns the number of snapshots outstanding which were created
// but not released. When snapshots are absent, mutable PutM()/DeleteM() can
// recycle nodes more aggressively. The record "excluded" is not counted.
func (t *Immutable) snapCount(excluded *SnapRecord) (count int, maxSnap, minSnap *SnapRecord) {
	// snapRecordMutex should be locked already

	count, maxSnap, minSnap = 0, nil, nil
	if t.snap == nil || *(t.snap) == nil {
		// No snapshots.
		return count, maxSnap, minSnap
	}

	// Count snapshots taken BEFORE creation of this instance.
	for h := *(t.snap); h != nil; h = h.prev {
		if h != excluded {
			count++
			if maxSnap == nil || maxSnap.generation < h.generation {
				maxSnap = h
			}
			if minSnap == nil || minSnap.generation > h.generation {
				minSnap = h
			}
		}
	}

	// Count snapshots taken AFTER creation of this instance.
	for h := (*(t.snap)).next; h != nil; h = h.next {
		if h != excluded {
			count++
			if maxSnap == nil || maxSnap.generation < h.generation {
				maxSnap = h
			}
			if minSnap == nil || minSnap.generation > h.generation {
				minSnap = h
			}
		}
	}

	return count, maxSnap, minSnap
}

func (t *Immutable) Recycle(excluded *SnapRecord) {
	snapRecordMutex.Lock()
	_, maxSnap, _ := t.snapCount(excluded)
	snapGen := 0
	if maxSnap != nil {
		snapGen = maxSnap.generation
	}
	snapRecordMutex.Unlock()

	var parents parentStack
	for node := t.root; node != nil; node = node.left {
		parents.Push(node)
	}

	for parents.Len() > 0 {
		node := parents.Pop()

		// Extend the nodes to traverse by all children to the left of
		// the current node's right child.
		for n := node.right; n != nil; n = n.left {
			parents.Push(n)
		}

		// Recycle node if it cannot be in a snapshot. Note that nodes
		// scheduled for deferred recycling will have negative generation
		// (recycleGeneration) and will not qualify.
		if node.generation > snapGen {
			putTreapNode(node)
		}
	}
}
