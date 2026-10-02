package cnki

import (
	"context"
	"encoding/json"
	"maps"
	"sync"
	"time"
)

type Budget struct {
	MaxRequests    int    `json:"max_requests"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	OnVerification string `json:"on_verification,omitempty"`
}

// OpRecord is the persisted and reported state of one operation.
type OpRecord struct {
	ID            string          `json:"operation_id"`
	Kind          string          `json:"kind"`
	Status        string          `json:"status"` // running, awaiting_user, completed, partial, failed, skipped, cancelled
	Stage         string          `json:"stage,omitempty"`
	Message       string          `json:"message,omitempty"`
	Progress      map[string]any  `json:"progress,omitempty"`
	Requests      int             `json:"requests"`
	ActiveSeconds float64         `json:"active_seconds"`
	Budget        Budget          `json:"budget"`
	Result        json.RawMessage `json:"result,omitempty"`
	Errors        []Problem       `json:"errors,omitempty"`
	Checkpoint    json.RawMessage `json:"checkpoint,omitempty"`
	Created       time.Time       `json:"created_at"`
	Updated       time.Time       `json:"updated_at"`
}

// Op is a running operation seen through one cancellation context. Workers of
// the same operation share opCore but may hold narrower child contexts.
type Op struct {
	ctx context.Context
	*opCore
}
type opCore struct {
	mu      sync.Mutex
	rec     OpRecord
	started time.Time
	base    time.Duration
	paused  time.Duration
	pauseAt time.Time
	cancel  context.CancelFunc
	done    chan struct{}
	resume  chan struct{}
	changed chan struct{}
	store   *Store
}

func (o *Op) Context() context.Context     { return o.ctx }
func (o *Op) With(ctx context.Context) *Op { return &Op{ctx: ctx, opCore: o.opCore} }
func (o *Op) ID() string                   { return o.rec.ID }
func (o *Op) policy() string               { o.mu.Lock(); defer o.mu.Unlock(); return o.rec.Budget.OnVerification }
func (o *opCore) activeLocked() time.Duration {
	return o.base + time.Since(o.started) - o.paused - o.pausing()
}
func (o *opCore) pausing() time.Duration {
	if o.pauseAt.IsZero() {
		return 0
	}
	return time.Since(o.pauseAt)
}
func (o *opCore) notifyLocked() {
	o.rec.Updated = time.Now().UTC()
	close(o.changed)
	o.changed = make(chan struct{})
}

// charge admits one site request against the operation's budget.
func (o *Op) charge() error {
	if err := o.ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	b := o.rec.Budget
	if b.MaxRequests > 0 && o.rec.Requests >= b.MaxRequests {
		return &Problem{Code: "BUDGET_EXHAUSTED", Message: "请求预算已用完；可用 operation_control continue 并增加预算", Retryable: true}
	}
	if b.TimeoutSeconds > 0 && o.activeLocked() > time.Duration(b.TimeoutSeconds)*time.Second {
		return &Problem{Code: "BUDGET_EXHAUSTED", Message: "活动时间预算已用完；可用 operation_control continue 并增加预算", Retryable: true}
	}
	o.rec.Requests++
	return nil
}
func (o *Op) stage(stage string, progress map[string]any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rec.Stage = stage
	if progress != nil {
		o.rec.Progress = progress
	}
	o.notifyLocked()
}
func (o *Op) problem(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rec.Errors = append(o.rec.Errors, *asProblem(err))
}
func (o *Op) checkpoint(v any) {
	data, _ := json.Marshal(v)
	o.mu.Lock()
	o.rec.Checkpoint = data
	rec := o.rec
	rec.ActiveSeconds = o.activeLocked().Seconds()
	o.mu.Unlock()
	if o.store != nil {
		o.store.PutOp(&rec) // a crash leaves a resumable checkpoint
	}
}

// awaitUser marks the operation as waiting for the person; time spent waiting
// is not charged to the activity budget.
func (o *Op) awaitUser(message string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rec.Status, o.rec.Message = "awaiting_user", message
	o.pauseAt = time.Now()
	o.resume = make(chan struct{}, 1)
	o.notifyLocked()
}
func (o *Op) userDone() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.pauseAt.IsZero() {
		o.paused += time.Since(o.pauseAt)
		o.pauseAt = time.Time{}
	}
	if o.rec.Status == "awaiting_user" {
		o.rec.Status = "running"
	}
	o.rec.Message = ""
	o.notifyLocked()
}
func (o *Op) resumeSignal() <-chan struct{} {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.resume
}
func (o *opCore) view() OpRecord {
	o.mu.Lock()
	defer o.mu.Unlock()
	r := o.rec
	r.Progress = maps.Clone(r.Progress)
	r.Errors = append([]Problem(nil), r.Errors...)
	r.ActiveSeconds = o.activeLocked().Seconds()
	if o.done != nil {
		select {
		case <-o.done:
			r.ActiveSeconds = o.rec.ActiveSeconds
		default:
		}
	}
	r.Checkpoint = nil
	return r
}

type Jobs struct {
	mu     sync.Mutex
	store  *Store
	active map[string]*Op
}

func NewJobs(store *Store) *Jobs { return &Jobs{store: store, active: map[string]*Op{}} }

// Start runs fn in the background. Operations share the site rate limiter, so
// concurrency here never increases the request rate seen by CNKI.
func (j *Jobs) Start(kind string, budget Budget, prior *OpRecord, fn func(*Op) (any, error)) *Op {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UTC()
	core := &opCore{store: j.store, started: time.Now(), cancel: cancel, done: make(chan struct{}), changed: make(chan struct{}), rec: OpRecord{ID: newID("op_"), Kind: kind, Status: "running", Stage: "starting", Budget: budget, Created: now, Updated: now}}
	if prior != nil {
		core.rec.ID, core.rec.Created, core.rec.Requests = prior.ID, prior.Created, prior.Requests
		core.base = time.Duration(prior.ActiveSeconds * float64(time.Second))
		core.rec.Checkpoint = prior.Checkpoint
	}
	op := &Op{ctx: ctx, opCore: core}
	j.mu.Lock()
	j.active[op.rec.ID] = op
	j.mu.Unlock()
	go func() {
		result, err := fn(op)
		core.mu.Lock()
		if result != nil {
			core.rec.Result, _ = json.Marshal(result)
		}
		switch {
		case ctx.Err() != nil:
			core.rec.Status = "cancelled"
		case err != nil:
			p := asProblem(err)
			core.rec.Errors = append(core.rec.Errors, *p)
			core.rec.Status = "failed"
			if result != nil || p.Retryable {
				core.rec.Status = "partial"
			}
			if p.Code == "VERIFICATION_SKIPPED" {
				core.rec.Status = "skipped"
			}
		case len(core.rec.Errors) > 0:
			core.rec.Status = "partial"
		default:
			core.rec.Status = "completed"
		}
		core.rec.Message = ""
		core.rec.ActiveSeconds = core.activeLocked().Seconds()
		core.notifyLocked()
		record := core.rec
		core.mu.Unlock()
		j.store.PutOp(&record)
		j.mu.Lock()
		delete(j.active, record.ID)
		j.mu.Unlock()
		close(core.done)
		cancel()
	}()
	return op
}

// Wait returns once the operation finishes, starts needing the user, or wait
// elapses. An operation already waiting for the user is waited on, so callers
// never spin while the person completes a verification.
func (j *Jobs) Wait(ctx context.Context, op *Op, wait time.Duration) OpRecord {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	op.mu.Lock()
	alreadyWaiting := op.rec.Status == "awaiting_user"
	op.mu.Unlock()
	for {
		op.mu.Lock()
		changed, status := op.changed, op.rec.Status
		op.mu.Unlock()
		select {
		case <-op.done:
			return j.finished(op)
		default:
		}
		if status == "awaiting_user" && !alreadyWaiting {
			return op.view()
		}
		if status != "awaiting_user" {
			alreadyWaiting = false
		}
		select {
		case <-op.done:
			return j.finished(op)
		case <-changed:
		case <-timer.C:
			return op.view()
		case <-ctx.Done():
			return op.view()
		}
	}
}
func (j *Jobs) finished(op *Op) OpRecord { return op.view() }
func (j *Jobs) Active(id string) (*Op, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if id == "" {
		for _, op := range j.active {
			return op, true
		}
		return nil, false
	}
	op, ok := j.active[id]
	return op, ok
}
func (j *Jobs) Status(ctx context.Context, id string, wait time.Duration) (OpRecord, error) {
	if op, ok := j.Active(id); ok {
		return j.Wait(ctx, op, wait), nil
	}
	if id == "" {
		return OpRecord{Status: "idle"}, nil
	}
	r, ok := j.store.Op(id)
	if !ok {
		return OpRecord{}, fail("OPERATION_NOT_FOUND", "任务不存在")
	}
	out := *r
	out.Checkpoint = nil
	if out.Status == "running" || out.Status == "awaiting_user" {
		out.Status = "interrupted"
		out.Message = "原进程已结束；检索可用 operation_control continue 继续"
	}
	return out, nil
}
func (j *Jobs) Cancel(ctx context.Context, id string) (OpRecord, error) {
	op, ok := j.Active(id)
	if !ok {
		return j.Status(ctx, id, 0)
	}
	op.cancel()
	select {
	case <-op.done:
	case <-time.After(5 * time.Second):
	}
	return op.view(), nil
}
func (j *Jobs) Resume(id string) error {
	op, ok := j.Active(id)
	if !ok {
		return fail("OPERATION_NOT_RUNNING", "任务不在当前进程运行")
	}
	op.mu.Lock()
	ch := op.resume
	if ch != nil && op.rec.Status == "awaiting_user" {
		op.rec.Status, op.rec.Message = "running", ""
		op.notifyLocked()
	}
	op.mu.Unlock()
	if ch == nil {
		return fail("OPERATION_STATE", "任务未等待人工处理")
	}
	select {
	case ch <- struct{}{}:
	default:
	}
	return nil
}
func (j *Jobs) Close() {
	j.mu.Lock()
	ops := make([]*Op, 0, len(j.active))
	for _, op := range j.active {
		ops = append(ops, op)
	}
	j.mu.Unlock()
	for _, op := range ops {
		op.cancel()
		<-op.done
	}
}
