package session

import "sort"

// Fold states for per-session tree folding.
const (
	FoldDefault = 0 // show direct children + grandchildren (2 levels)
	FoldFull    = 1 // show all descendants
	FoldClosed  = 2 // hide all descendants
)

// MaxTreeDepth is the maximum tree depth supported.
const MaxTreeDepth = 5

// SessionKey returns the unique key for a session, including host prefix for remote sessions.
func SessionKey(host, fullName string) string {
	if host == "" {
		return fullName
	}
	return host + ":" + fullName
}

// BuildTree arranges sessions into a parent-child tree structure.
// parents maps session key (SessionKey) to parent key.
// foldState maps session key to fold state (FoldDefault/FoldFull/FoldClosed).
// Returns a flat list ordered for tree display with TreeDepth, TreePrefix, and Virtual fields set.
// Sessions hidden by fold state are appended at the end with TreeHidden=true.
func BuildTree(sessions []Session, parents map[string]string, foldState map[string]int) []Session {
	if foldState == nil {
		foldState = make(map[string]int)
	}

	// Build lookup of active sessions by key
	active := make(map[string]*Session)
	for i := range sessions {
		key := SessionKey(sessions[i].Host, sessions[i].FullName)
		active[key] = &sessions[i]
	}

	// Assign Parent field from DB
	for i := range sessions {
		key := SessionKey(sessions[i].Host, sessions[i].FullName)
		if p, ok := parents[key]; ok {
			sessions[i].Parent = p
		}
	}

	// Build parent→children map
	children := make(map[string][]int) // parent key → indices in sessions
	orphans := make([]int, 0)

	for i := range sessions {
		if sessions[i].Parent == "" {
			orphans = append(orphans, i)
		} else {
			children[sessions[i].Parent] = append(children[sessions[i].Parent], i)
		}
	}

	// Identify virtual parents: referenced as parent but not in active sessions
	virtualParents := make(map[string]bool)
	for parentKey := range children {
		if _, exists := active[parentKey]; !exists {
			virtualParents[parentKey] = true
		}
	}

	// Sort children within each group by status priority, then duration
	for key := range children {
		idxs := children[key]
		sort.SliceStable(idxs, func(a, b int) bool {
			sa, sb := sessions[idxs[a]], sessions[idxs[b]]
			pa, pb := statusPriority(sa.Status), statusPriority(sb.Status)
			if pa != pb {
				return pa < pb
			}
			return sa.Duration < sb.Duration
		})
		children[key] = idxs
	}

	// Collect all root-level entries
	type rootEntry struct {
		key       string
		session   *Session // nil for virtual parents
		isVirtual bool
		idx       int // index in sessions slice (-1 for virtual)
	}

	var roots []rootEntry
	for _, i := range orphans {
		key := SessionKey(sessions[i].Host, sessions[i].FullName)
		roots = append(roots, rootEntry{key: key, session: &sessions[i], idx: i})
	}
	for key := range virtualParents {
		roots = append(roots, rootEntry{key: key, isVirtual: true, idx: -1})
	}

	// Sort roots: real sessions first (by status priority), virtual after
	sort.SliceStable(roots, func(a, b int) bool {
		if roots[a].isVirtual != roots[b].isVirtual {
			return !roots[a].isVirtual
		}
		if roots[a].isVirtual && roots[b].isVirtual {
			return roots[a].key < roots[b].key
		}
		sa, sb := roots[a].session, roots[b].session
		aLocal := sa.Host == ""
		bLocal := sb.Host == ""
		if aLocal != bLocal {
			return aLocal
		}
		pa, pb := statusPriority(sa.Status), statusPriority(sb.Status)
		if pa != pb {
			return pa < pb
		}
		return sa.Duration < sb.Duration
	})

	// Helper: count all descendants recursively
	var countDescendants func(key string) int
	countDescendants = func(key string) int {
		kids := children[key]
		count := len(kids)
		for _, idx := range kids {
			childKey := SessionKey(sessions[idx].Host, sessions[idx].FullName)
			count += countDescendants(childKey)
		}
		return count
	}

	// Helper: visible levels for a session based on fold state
	visibleLevels := func(key string) int {
		switch foldState[key] {
		case FoldFull:
			return MaxTreeDepth
		case FoldClosed:
			return 0
		default:
			return 2
		}
	}

	// Track which session indices were included in the visible tree
	included := make(map[int]bool)

	// Build the flat output list
	result := make([]Session, 0, len(sessions)+len(virtualParents))

	// Recursive helper to add a subtree's children
	var addSubtree func(parentKey string, remainingLevels int, isLastStack []bool)
	addSubtree = func(parentKey string, remainingLevels int, isLastStack []bool) {
		kids := children[parentKey]
		if len(kids) == 0 || remainingLevels <= 0 {
			return
		}

		for ci, childIdx := range kids {
			child := sessions[childIdx]
			isLast := ci == len(kids)-1
			depth := len(isLastStack) + 1
			child.TreeDepth = depth
			child.TreeHidden = false
			child.HiddenCount = 0

			// Build prefix from isLastStack
			prefix := ""
			for _, ancestorIsLast := range isLastStack {
				if ancestorIsLast {
					prefix += "    "
				} else {
					prefix += "│   "
				}
			}
			if isLast {
				prefix += "└── "
			} else {
				prefix += "├── "
			}
			child.TreePrefix = prefix

			childKey := SessionKey(child.Host, child.FullName)

			// Determine how deep to recurse into this child's subtree
			childRemaining := remainingLevels - 1
			if _, hasFold := foldState[childKey]; hasFold {
				childRemaining = visibleLevels(childKey)
			}

			// Clamp by MaxTreeDepth (absolute depth limit)
			if depth+childRemaining > MaxTreeDepth {
				childRemaining = MaxTreeDepth - depth
			}
			if childRemaining < 0 {
				childRemaining = 0
			}

			// Add child to result
			result = append(result, child)
			included[childIdx] = true
			childResultIdx := len(result) - 1

			// Recurse into child's subtree
			newStack := make([]bool, len(isLastStack)+1)
			copy(newStack, isLastStack)
			newStack[len(isLastStack)] = isLast
			addSubtree(childKey, childRemaining, newStack)

			// Compute hidden count: total descendants minus actually shown
			totalDesc := countDescendants(childKey)
			added := len(result) - childResultIdx - 1
			hidden := totalDesc - added
			if hidden > 0 {
				result[childResultIdx].HiddenCount = hidden
			}
		}
	}

	// Build output from roots
	for _, root := range roots {
		rootKey := root.key
		remaining := visibleLevels(rootKey)

		if root.isVirtual {
			name, host := parseSessionKey(root.key)
			vs := Session{
				Name:     stripPrefix(name),
				FullName: name,
				Host:     host,
				Virtual:  true,
			}
			result = append(result, vs)
		} else {
			s := sessions[root.idx]
			s.TreeDepth = 0
			s.TreePrefix = ""
			s.TreeHidden = false
			s.HiddenCount = 0
			result = append(result, s)
			included[root.idx] = true
		}

		rootResultIdx := len(result) - 1
		addSubtree(rootKey, remaining, nil)

		// Compute hidden count for root
		totalDesc := countDescendants(rootKey)
		added := len(result) - rootResultIdx - 1
		hidden := totalDesc - added
		if hidden > 0 {
			result[rootResultIdx].HiddenCount = hidden
		}
	}

	// Append sessions not included in the visible tree (hidden by fold state).
	// Skip virtual parents from previous builds — they'll be re-created if needed.
	for i := range sessions {
		if !included[i] && !sessions[i].Virtual {
			s := sessions[i]
			s.TreeHidden = true
			result = append(result, s)
		}
	}

	return result
}

// parseSessionKey splits "host:fullName" back into parts.
func parseSessionKey(key string) (fullName, host string) {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return key[i+1:], key[:i]
		}
	}
	return key, ""
}

// stripPrefix removes common session prefixes for display.
func stripPrefix(fullName string) string {
	if len(fullName) > 5 && fullName[:5] == "crab-" {
		return fullName[5:]
	}
	return fullName
}
