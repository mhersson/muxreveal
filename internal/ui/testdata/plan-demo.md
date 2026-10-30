# Session Caching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cache each session's token counts so the rail can show them without reading the transcript again.

**Architecture:**
- A `Counts` store keeps one entry per session id.
- The hook handler writes it and the rail only reads it.

## Task 1: The store

**Files:**
- Add: `internal/core/counts.go`
- Test: `internal/core/counts_test.go`

- [x] **Step 1:** Write the failing test for `Counts.Add`.
- [x] **Step 2:** Run it and watch it fail.
- [ ] **Step 3:** Implement `Counts` with a mutex around the map.
- [ ] **Step 4:** Run `make check-fast` and commit.

```go
type Counts struct {
	mu sync.Mutex
	by map[SessionID]int
}

func (c *Counts) Add(id SessionID, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.by[id] += n
}
```

## Task 2: The rail

The rail reads `Counts` once per tick, so a burst of hooks costs one read.

<details>
<summary>For reviewers</summary>

Check that the tick never takes the lock while it draws, and that the `Counts` zero value is safe to read.

</details>

### Notes

- Keep the entry when a session ends, so a resumed session starts from its count.
- Drop entries for panes that are gone.
