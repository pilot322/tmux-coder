package domain

import "fmt"

type SessionReparent struct {
	SessionID int
	ParentID  int
}

type WorktreeRemovalPlan struct {
	DeleteSessionIDs  []int
	ReparentWorktrees []SessionReparent
}

type SecondaryRemovalPlan struct {
	DeleteSessionIDs    []int
	ReparentSecondaries []SessionReparent
}

func PlanWorktreeRemoval(sessions []*Session, removedID int) (WorktreeRemovalPlan, error) {
	idx, err := newTopologyIndex(sessions)
	if err != nil {
		return WorktreeRemovalPlan{}, err
	}
	removed, err := idx.session(removedID, WorktreeSession)
	if err != nil {
		return WorktreeRemovalPlan{}, err
	}
	if err := idx.validateKnownParentChain(removed); err != nil {
		return WorktreeRemovalPlan{}, err
	}

	deleteIDs, err := idx.secondaryDescendantIDs(removedID)
	if err != nil {
		return WorktreeRemovalPlan{}, err
	}
	deleteIDs = append(deleteIDs, removedID)

	var reparent []SessionReparent
	for _, child := range idx.children[removedID] {
		if child.Type() == WorktreeSession {
			reparent = append(reparent, SessionReparent{SessionID: child.ID(), ParentID: removed.Parent()})
		}
	}
	return WorktreeRemovalPlan{DeleteSessionIDs: deleteIDs, ReparentWorktrees: reparent}, nil
}

func PlanWorktreePrune(sessions []*Session, prunedIDs []int) (WorktreeRemovalPlan, error) {
	idx, err := newTopologyIndex(sessions)
	if err != nil {
		return WorktreeRemovalPlan{}, err
	}
	pruned := make(map[int]bool, len(prunedIDs))
	for _, id := range prunedIDs {
		if _, err := idx.session(id, WorktreeSession); err != nil {
			return WorktreeRemovalPlan{}, err
		}
		pruned[id] = true
	}

	var deleteIDs []int
	for _, id := range prunedIDs {
		secondaryIDs, err := idx.secondaryDescendantIDs(id)
		if err != nil {
			return WorktreeRemovalPlan{}, err
		}
		deleteIDs = append(deleteIDs, secondaryIDs...)
	}
	deleteIDs = append(deleteIDs, worktreePostorder(prunedIDs, idx.byID, pruned)...)

	var reparent []SessionReparent
	for _, s := range sessions {
		if s.Type() != WorktreeSession || pruned[s.ID()] || !pruned[s.Parent()] {
			continue
		}
		newParent := s.Parent()
		seen := map[int]bool{s.ID(): true}
		for newParent > 0 && pruned[newParent] {
			if seen[newParent] {
				return WorktreeRemovalPlan{}, fmt.Errorf("session topology cycle at session %d", newParent)
			}
			seen[newParent] = true
			parent, ok := idx.byID[newParent]
			if !ok {
				newParent = -1
				break
			}
			newParent = parent.Parent()
		}
		if newParent > 0 {
			if _, ok := idx.byID[newParent]; !ok {
				newParent = -1
			}
		}
		reparent = append(reparent, SessionReparent{SessionID: s.ID(), ParentID: newParent})
	}
	return WorktreeRemovalPlan{DeleteSessionIDs: deleteIDs, ReparentWorktrees: reparent}, nil
}

func PlanSecondaryRemoval(sessions []*Session, removedID int) (SecondaryRemovalPlan, error) {
	idx, err := newTopologyIndex(sessions)
	if err != nil {
		return SecondaryRemovalPlan{}, err
	}
	removed, err := idx.session(removedID, SecondarySession)
	if err != nil {
		return SecondaryRemovalPlan{}, err
	}

	if removed.OnDelete() == "inherit" {
		var reparent []SessionReparent
		for _, child := range idx.children[removedID] {
			if child.Type() != SecondarySession {
				return SecondaryRemovalPlan{}, fmt.Errorf("worktree session %d cannot be child of secondary session %d", child.ID(), removedID)
			}
			reparent = append(reparent, SessionReparent{SessionID: child.ID(), ParentID: removed.Parent()})
		}
		return SecondaryRemovalPlan{DeleteSessionIDs: []int{removedID}, ReparentSecondaries: reparent}, nil
	}

	deleteIDs, err := idx.secondaryDescendantIDs(removedID)
	if err != nil {
		return SecondaryRemovalPlan{}, err
	}
	deleteIDs = append(deleteIDs, removedID)
	return SecondaryRemovalPlan{DeleteSessionIDs: deleteIDs}, nil
}

type topologyIndex struct {
	byID     map[int]*Session
	children map[int][]*Session
}

func newTopologyIndex(sessions []*Session) (*topologyIndex, error) {
	idx := &topologyIndex{byID: make(map[int]*Session, len(sessions)), children: make(map[int][]*Session)}
	for _, s := range sessions {
		if s == nil {
			continue
		}
		if _, exists := idx.byID[s.ID()]; exists {
			return nil, fmt.Errorf("duplicate session id %d", s.ID())
		}
		idx.byID[s.ID()] = s
	}
	for _, s := range sessions {
		if s == nil || s.Parent() <= 0 {
			continue
		}
		idx.children[s.Parent()] = append(idx.children[s.Parent()], s)
	}
	for _, s := range sessions {
		if s == nil {
			continue
		}
		if err := idx.validateKnownParentCycles(s); err != nil {
			return nil, err
		}
	}
	for _, s := range sessions {
		if s != nil && s.Type() == SecondarySession {
			for _, child := range idx.children[s.ID()] {
				if child.Type() == WorktreeSession {
					return nil, fmt.Errorf("worktree session %d cannot be child of secondary session %d", child.ID(), s.ID())
				}
			}
		}
	}
	return idx, nil
}

func (idx *topologyIndex) validateKnownParentCycles(s *Session) error {
	seen := map[int]bool{s.ID(): true}
	for parentID := s.Parent(); parentID > 0; {
		if seen[parentID] {
			return fmt.Errorf("session topology cycle at session %d", parentID)
		}
		seen[parentID] = true
		parent, ok := idx.byID[parentID]
		if !ok {
			return nil
		}
		parentID = parent.Parent()
	}
	return nil
}

func (idx *topologyIndex) session(id int, kind SessionType) (*Session, error) {
	s, ok := idx.byID[id]
	if !ok {
		return nil, fmt.Errorf("session %d not found", id)
	}
	if s.Type() != kind {
		return nil, fmt.Errorf("session %d has type %d, want %d", id, s.Type(), kind)
	}
	return s, nil
}

func (idx *topologyIndex) validateKnownParentChain(s *Session) error {
	seen := map[int]bool{s.ID(): true}
	for parentID := s.Parent(); parentID > 0; {
		if seen[parentID] {
			return fmt.Errorf("session topology cycle at session %d", parentID)
		}
		seen[parentID] = true
		parent, ok := idx.byID[parentID]
		if !ok {
			return fmt.Errorf("session %d has missing parent %d", s.ID(), parentID)
		}
		parentID = parent.Parent()
	}
	return nil
}

func (idx *topologyIndex) secondaryDescendantIDs(parentID int) ([]int, error) {
	var out []int
	visiting := make(map[int]bool)
	var walk func(int) error
	walk = func(id int) error {
		if visiting[id] {
			return fmt.Errorf("session topology cycle at session %d", id)
		}
		visiting[id] = true
		defer delete(visiting, id)
		for _, child := range idx.children[id] {
			parent := idx.byID[id]
			if child.Type() == WorktreeSession && parent != nil && parent.Type() == SecondarySession {
				return fmt.Errorf("worktree session %d cannot be child of secondary session %d", child.ID(), id)
			}
			if child.Type() != SecondarySession {
				continue
			}
			if err := walk(child.ID()); err != nil {
				return err
			}
			out = append(out, child.ID())
		}
		return nil
	}
	return out, walk(parentID)
}

func worktreePostorder(ids []int, byID map[int]*Session, included map[int]bool) []int {
	seen := make(map[int]bool, len(ids))
	var out []int
	var visit func(int)
	visit = func(id int) {
		if seen[id] {
			return
		}
		seen[id] = true
		for _, childID := range ids {
			child := byID[childID]
			if child != nil && child.Parent() == id && included[childID] {
				visit(childID)
			}
		}
		out = append(out, id)
	}
	for _, id := range ids {
		visit(id)
	}
	return out
}
