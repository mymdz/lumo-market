package app

import "time"

type evKind uint8

const (
	evSession   evKind = iota + 1 // shopping session of a customer / visitor
	evCart                        // cart decision or recovery
	evOrder                       // order lifecycle step
	evPO                          // purchase order step
	evReplenish                   // consumable re-purchase session
)

// event is a scheduled simulation event. Aggregates keep their own NextAt;
// stale events (entity rescheduled) are detected by comparing times.
type event struct {
	at   int64 // unix nanos
	seq  uint64
	kind evKind
	ref  any
	aux  int64
}

// eventQueue is a binary min-heap ordered by (at, seq).
type eventQueue struct {
	h   []event
	seq uint64
}

func (q *eventQueue) Len() int { return len(q.h) }

func (q *eventQueue) less(i, j int) bool {
	if q.h[i].at != q.h[j].at {
		return q.h[i].at < q.h[j].at
	}
	return q.h[i].seq < q.h[j].seq
}

func (q *eventQueue) Push(at time.Time, kind evKind, ref any, aux int64) {
	q.seq++
	q.h = append(q.h, event{at: at.UnixNano(), seq: q.seq, kind: kind, ref: ref, aux: aux})
	i := len(q.h) - 1
	for i > 0 {
		p := (i - 1) / 2
		if !q.less(i, p) {
			break
		}
		q.h[i], q.h[p] = q.h[p], q.h[i]
		i = p
	}
}

func (q *eventQueue) PeekAt() (int64, bool) {
	if len(q.h) == 0 {
		return 0, false
	}
	return q.h[0].at, true
}

func (q *eventQueue) Pop() event {
	top := q.h[0]
	n := len(q.h) - 1
	q.h[0] = q.h[n]
	q.h[n] = event{}
	q.h = q.h[:n]
	i := 0
	for {
		l, r := 2*i+1, 2*i+2
		m := i
		if l < n && q.less(l, m) {
			m = l
		}
		if r < n && q.less(r, m) {
			m = r
		}
		if m == i {
			break
		}
		q.h[i], q.h[m] = q.h[m], q.h[i]
		i = m
	}
	return top
}
