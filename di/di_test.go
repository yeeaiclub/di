package di

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// ============================================================================
// 测试类型与工厂
// ============================================================================

// 基础依赖链：svcT -> {repoT -> daoT -> sess, otherD}
type sess struct{ id int }
type daoT struct{ s *sess }
type repoT struct{ d *daoT }
type otherD struct{ tag string }
type svcT struct {
	r *repoT
	o *otherD
}

func mkSess(id int) func() *sess { return func() *sess { return &sess{id: id} } }
func mkDao(s *sess) *daoT        { return &daoT{s: s} }
func mkRepo(d *daoT) *repoT      { return &repoT{d: d} }
func mkOther() *otherD           { return &otherD{tag: "o"} }
func mkSvc(r *repoT, o *otherD) *svcT {
	return &svcT{r: r, o: o}
}

// 菱形：top -> {left -> bottom, right -> bottom}，bottom 必须是单例
type diaBottom struct{ v int }
type diaLeft struct{ b *diaBottom }
type diaRight struct{ b *diaBottom }
type diaTop struct {
	l *diaLeft
	r *diaRight
}

// ============================================================================
// 1. Container 自身
// ============================================================================

func TestNew_ReturnsNonNilContainer(t *testing.T) {
	if New() == nil {
		t.Fatal("New() returned nil")
	}
}

func TestEmptyContainer_ResolveFailsWithNotFound(t *testing.T) {
	c := New()
	_, err := Resolve[*sess](c)
	if err == nil {
		t.Fatal("expected error from empty container")
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err type = %T, want *NotFoundError", err)
	}
	if nf.Type != reflect.TypeOf((*sess)(nil)) {
		t.Errorf("nf.Type = %v, want *di.sess", nf.Type)
	}
	if !strings.Contains(nf.Error(), "sess") {
		t.Errorf("error msg = %q, want contains 'sess'", nf.Error())
	}
}

func TestMultipleContainers_AreIsolated(t *testing.T) {
	c1 := New()
	c2 := New()
	D[*sess](c1, mkSess(1))
	D[*sess](c2, mkSess(2))

	if v := MustResolve[*sess](c1); v.id != 1 {
		t.Errorf("c1 id = %d, want 1", v.id)
	}
	if v := MustResolve[*sess](c2); v.id != 2 {
		t.Errorf("c2 id = %d, want 2", v.id)
	}
}

// ============================================================================
// 2. Depends / Dep：注册期
// ============================================================================

func TestDepends_ReturnsHandleWithSameContainer(t *testing.T) {
	c := New()
	d := D[*sess](c, mkSess(1))
	if d == nil {
		t.Fatal("Depends returned nil")
	}
	if d.Container() != c {
		t.Fatal("d.Container() != c")
	}
}

func TestDepends_NilContainer_Panics(t *testing.T) {
	defer expectPanic(t, "expected panic for nil container")
	D[*sess](nil, mkSess(1))
}

func TestDepends_NilFn_Panics(t *testing.T) {
	defer expectPanic(t, "expected panic for nil fn")
	D[*sess](New(), nil)
}

func TestDepends_NotAFunction_Panics(t *testing.T) {
	defer expectPanic(t, "expected panic for non-function")
	D[*sess](New(), "not a function")
}

func TestDepends_DuplicateType_Panics(t *testing.T) {
	c := New()
	D[*sess](c, mkSess(1))
	defer expectPanic(t, "expected panic for duplicate registration")
	D[*sess](c, mkSess(2))
}

func TestDepends_UnsupportedReturnArity_ReturnsError(t *testing.T) {
	c := New()
	D[*sess](c, func() (*sess, error, string) { return nil, nil, "x" })
	_, err := Resolve[*sess](c)
	if err == nil {
		t.Fatal("expected error for 3 return values")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("err = %v", err)
	}
}

// ============================================================================
// 3. Depends：解析期 - 正常路径
// ============================================================================

func TestDepends_ZeroArgFactory(t *testing.T) {
	c := New()
	d := D[*sess](c, mkSess(99))
	if v := d.MustGet(); v.id != 99 {
		t.Fatalf("id = %d, want 99", v.id)
	}
}

func TestDepends_LinearChain_AllInjected(t *testing.T) {
	c := New()
	D[*sess](c, mkSess(42))
	D[*daoT](c, mkDao)
	D[*repoT](c, mkRepo)
	D[*otherD](c, mkOther)

	svc := D[*svcT](c, mkSvc).MustGet()
	if svc.r.d.s.id != 42 {
		t.Errorf("sess.id = %d, want 42", svc.r.d.s.id)
	}
	if svc.o.tag != "o" {
		t.Errorf("otherD.tag = %q, want o", svc.o.tag)
	}
	// 验证每一层都是同一个指针（单例）
	if svc.r.d.s != MustResolve[*sess](c) {
		t.Error("sess instance not shared across dep tree")
	}
}

// 深链：10 层
func TestDepends_DeepChain(t *testing.T) {
	type l0 struct{ Tag int }
	type l1 struct{ A *l0 }
	type l2 struct{ A *l1 }
	type l3 struct{ A *l2 }
	type l4 struct{ A *l3 }
	type l5 struct{ A *l4 }
	type l6 struct{ A *l5 }
	type l7 struct{ A *l6 }
	type l8 struct{ A *l7 }
	type l9 struct{ A *l8 }
	type l10 struct{ A *l9 }

	c := New()
	D[*l0](c, func() *l0 { return &l0{Tag: 0} })
	D[*l1](c, func(a *l0) *l1 { return &l1{A: a} })
	D[*l2](c, func(a *l1) *l2 { return &l2{A: a} })
	D[*l3](c, func(a *l2) *l3 { return &l3{A: a} })
	D[*l4](c, func(a *l3) *l4 { return &l4{A: a} })
	D[*l5](c, func(a *l4) *l5 { return &l5{A: a} })
	D[*l6](c, func(a *l5) *l6 { return &l6{A: a} })
	D[*l7](c, func(a *l6) *l7 { return &l7{A: a} })
	D[*l8](c, func(a *l7) *l8 { return &l8{A: a} })
	D[*l9](c, func(a *l8) *l9 { return &l9{A: a} })

	top := D[*l10](c, func(a *l9) *l10 { return &l10{A: a} }).MustGet()

	// 通过 reflect 走 9 层
	cur := any(top)
	depth := 0
	for {
		v := reflect.ValueOf(cur)
		if v.Kind() != reflect.Ptr || v.IsNil() {
			break
		}
		v = v.Elem()
		if v.Kind() != reflect.Struct || v.NumField() == 0 {
			t.Fatalf("unexpected shape at depth %d", depth)
		}
		nxt := v.Field(0)
		if nxt.Kind() != reflect.Ptr || nxt.IsNil() {
			break
		}
		cur = nxt.Interface()
		depth++
	}
	if depth != 10 {
		t.Errorf("walked %d levels, want 10", depth)
	}
}

// 宽依赖：同一层 5 个不同类型
func TestDepends_WideDeps(t *testing.T) {
	type w1 struct{ v int }
	type w2 struct{ v int }
	type w3 struct{ v int }
	type w4 struct{ v int }
	type w5 struct{ v int }
	type wide struct {
		a *w1
		b *w2
		c *w3
		d *w4
		e *w5
	}

	c := New()
	D[*w1](c, func() *w1 { return &w1{v: 1} })
	D[*w2](c, func() *w2 { return &w2{v: 2} })
	D[*w3](c, func() *w3 { return &w3{v: 3} })
	D[*w4](c, func() *w4 { return &w4{v: 4} })
	D[*w5](c, func() *w5 { return &w5{v: 5} })

	w := D[*wide](c, func(a *w1, b *w2, c *w3, d *w4, e *w5) *wide {
		return &wide{a: a, b: b, c: c, d: d, e: e}
	}).MustGet()
	if w.a.v+w.b.v+w.c.v+w.d.v+w.e.v != 15 {
		t.Errorf("sum = %d, want 15", w.a.v+w.b.v+w.c.v+w.d.v+w.e.v)
	}
}

// 菱形依赖 + 共享单例
func TestDepends_DiamondDependency_SharesSingleton(t *testing.T) {
	var cnt int32
	c := New()
	D[*diaBottom](c, func() *diaBottom {
		atomic.AddInt32(&cnt, 1)
		return &diaBottom{v: 7}
	})
	D[*diaLeft](c, func(b *diaBottom) *diaLeft { return &diaLeft{b: b} })
	D[*diaRight](c, func(b *diaBottom) *diaRight { return &diaRight{b: b} })
	D[*diaTop](c, func(l *diaLeft, r *diaRight) *diaTop {
		return &diaTop{l: l, r: r}
	})

	top := MustResolve[*diaTop](c)

	if got := atomic.LoadInt32(&cnt); got != 1 {
		t.Errorf("bottom constructed %d times, want 1", got)
	}
	if top.l.b != top.r.b {
		t.Error("left.b and right.b are different pointers (diamond broken)")
	}
}

// ============================================================================
// 4. 单例语义
// ============================================================================

func TestSingleton_DependencyMemoized(t *testing.T) {
	var cnt int32
	c := New()
	D[*sess](c, func() *sess {
		atomic.AddInt32(&cnt, 1)
		return &sess{id: 1}
	})

	d := D[*daoT](c, mkDao)
	a := d.MustGet()
	b := d.MustGet()
	if a != b {
		t.Fatal("dao instances differ")
	}
	if got := atomic.LoadInt32(&cnt); got != 1 {
		t.Errorf("sess constructed %d times, want 1", got)
	}
}

func TestSingleton_FactoryInvokedOnce_EvenViaResolve(t *testing.T) {
	var cnt int32
	c := New()
	D[*sess](c, func() *sess {
		atomic.AddInt32(&cnt, 1)
		return &sess{id: 1}
	})
	for i := 0; i < 50; i++ {
		_ = MustResolve[*sess](c)
	}
	if got := atomic.LoadInt32(&cnt); got != 1 {
		t.Errorf("factory called %d times, want 1", got)
	}
}

// ============================================================================
// 5. 错误处理
// ============================================================================

func TestNotFoundError_TypeAndMessage(t *testing.T) {
	_, err := Resolve[*sess](New())
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err type = %T, want *NotFoundError", err)
	}
	want := reflect.TypeOf((*sess)(nil))
	if nf.Type != want {
		t.Errorf("nf.Type = %v, want %v", nf.Type, want)
	}
	if !strings.Contains(nf.Error(), "*di.sess") {
		t.Errorf("err = %q", nf.Error())
	}
}

func TestNotFoundError_PropagatesThroughChain(t *testing.T) {
	c := New()
	D[*sess](c, mkSess(1))
	D[*daoT](c, mkDao)
	// 故意不注册 *repoT / *otherD / *svcT

	// mkSvc 依赖 *repoT 和 *otherD，二者都没注册
	_, err := Invoke(c, mkSvc)
	if err == nil {
		t.Fatal("expected error for missing *repoT")
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, expected *NotFoundError", err)
	}
	// 错误类型应该指向第一个无法解析的参数
	if nf.Type != reflect.TypeOf((*repoT)(nil)) {
		t.Errorf("nf.Type = %v, want *di.repoT", nf.Type)
	}
	// 错误链上要带「parameter 0」这种上下文
	if !strings.Contains(err.Error(), "parameter") {
		t.Errorf("err = %q, want contains 'parameter'", err.Error())
	}
}

func TestFactoryError_WrappedWithParamContext(t *testing.T) {
	c := New()
	custom := errors.New("db down")
	D[*sess](c, func() (*sess, error) { return nil, custom })
	D[*daoT](c, mkDao)

	_, err := Resolve[*daoT](c)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, custom) {
		t.Fatalf("err = %v, want wraps %v", err, custom)
	}
	if !strings.Contains(err.Error(), "parameter 0") {
		t.Errorf("err msg should mention parameter 0, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "*di.sess") {
		t.Errorf("err msg should mention *di.sess, got %q", err.Error())
	}
}

func TestFactoryError_BareT(t *testing.T) {
	c := New()
	custom := errors.New("nope")
	D[*sess](c, func() (*sess, error) { return nil, custom })
	_, err := Resolve[*sess](c)
	if !errors.Is(err, custom) {
		t.Fatalf("err = %v, want wraps %v", err, custom)
	}
}

func TestReturnTypeMismatch_AtResolve(t *testing.T) {
	c := New()
	D[*sess](c, func() *daoT { return &daoT{} })

	_, err := Resolve[*sess](c)
	if err == nil {
		t.Fatal("expected type mismatch error")
	}
	if !strings.Contains(err.Error(), "cannot assert") {
		t.Errorf("err = %v, want contains 'cannot assert'", err)
	}
}

func TestFactoryReturnsNil_TypedError(t *testing.T) {
	c := New()
	D[*sess](c, func() *sess { return nil })

	_, err := Resolve[*sess](c)
	if err == nil {
		t.Fatal("expected nil-return error")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("err = %v", err)
	}
}

// ============================================================================
// 6. 值类型（非指针）
// ============================================================================

func TestValueType_NotPointer(t *testing.T) {
	type cfg struct{ port int }
	c := New()
	D[cfg](c, func() cfg { return cfg{port: 8080} })

	v, err := Resolve[cfg](c)
	if err != nil {
		t.Fatal(err)
	}
	if v.port != 8080 {
		t.Fatalf("port = %d, want 8080", v.port)
	}
	// 第二次解析应该拿到等价的值（值类型没有指针可比）
	if v2 := MustResolve[cfg](c); v2.port != 8080 {
		t.Fatalf("port = %d", v2.port)
	}
}

func TestValueType_ChainedDep(t *testing.T) {
	type base struct{ id int }
	type derived struct{ b base }
	c := New()
	D[base](c, func() base { return base{id: 3} })
	D[derived](c, func(b base) derived { return derived{b: b} })

	v, err := Resolve[derived](c)
	if err != nil {
		t.Fatal(err)
	}
	if v.b.id != 3 {
		t.Fatalf("id = %d", v.b.id)
	}
}

// ============================================================================
// 7. Resolve / MustResolve
// ============================================================================

func TestResolve_GenericAPI(t *testing.T) {
	c := New()
	D[*sess](c, mkSess(11))
	v, err := Resolve[*sess](c)
	if err != nil {
		t.Fatal(err)
	}
	if v.id != 11 {
		t.Fatalf("id = %d, want 11", v.id)
	}
}

func TestMustResolve_PanicsOnError(t *testing.T) {
	defer expectPanic(t, "expected panic from MustResolve")
	MustResolve[*sess](New())
}

func TestMustResolve_ReturnsValueOnSuccess(t *testing.T) {
	c := New()
	D[*sess](c, mkSess(7))
	v := MustResolve[*sess](c)
	if v.id != 7 {
		t.Fatalf("id = %d", v.id)
	}
}

// ============================================================================
// 8. Invoke / Call
// ============================================================================

func TestInvoke_AutoInjectsParams(t *testing.T) {
	c := New()
	D[*sess](c, mkSess(3))
	D[*daoT](c, mkDao)

	results, err := Invoke(c, mkDao)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
	d, ok := results[0].(*daoT)
	if !ok {
		t.Fatalf("type = %T, want *daoT", results[0])
	}
	if d.s.id != 3 {
		t.Fatalf("sess.id = %d", d.s.id)
	}
}

func TestInvoke_NilContainer_Errors(t *testing.T) {
	_, err := Invoke(nil, mkSess(1))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInvoke_NilFn_Errors(t *testing.T) {
	_, err := Invoke(New(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInvoke_NotAFunction_Errors(t *testing.T) {
	_, err := Invoke(New(), 42)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInvoke_MissingDep_Errors(t *testing.T) {
	_, err := Invoke(New(), mkDao) // *sess 未注册
	if err == nil {
		t.Fatal("expected error")
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err type = %T, want *NotFoundError", err)
	}
}

func TestInvoke_NoArgsNoDeps(t *testing.T) {
	c := New()
	results, err := Invoke(c, mkSess(5))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d", len(results))
	}
	if results[0].(*sess).id != 5 {
		t.Fatalf("id = %d", results[0].(*sess).id)
	}
}

func TestCall_OneReturn(t *testing.T) {
	c := New()
	v, err := Call[*sess](c, func() *sess { return &sess{id: 12} })
	if err != nil {
		t.Fatal(err)
	}
	if v.id != 12 {
		t.Fatalf("id = %d", v.id)
	}
}

func TestCall_TwoReturn_Success(t *testing.T) {
	c := New()
	v, err := Call[*sess](c, func() (*sess, error) { return &sess{id: 13}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if v.id != 13 {
		t.Fatalf("id = %d", v.id)
	}
}

func TestCall_TwoReturn_ErrorPropagates(t *testing.T) {
	c := New()
	custom := errors.New("bad")
	_, err := Call[*sess](c, func() (*sess, error) { return nil, custom })
	if !errors.Is(err, custom) {
		t.Fatalf("err = %v, want %v", err, custom)
	}
}

func TestCall_NoReturn_Errors(t *testing.T) {
	c := New()
	_, err := Call[*sess](c, func() {})
	if err == nil {
		t.Fatal("expected error for zero returns")
	}
}

func TestCall_ThreeReturn_Errors(t *testing.T) {
	c := New()
	_, err := Call[*sess](c, func() (*sess, error, error) {
		return nil, nil, nil
	})
	if err == nil {
		t.Fatal("expected error for 3 returns")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("err = %v", err)
	}
}

func TestCall_TypeMismatch_Errors(t *testing.T) {
	c := New()
	_, err := Call[*daoT](c, func() *sess { return &sess{} })
	if err == nil {
		t.Fatal("expected type mismatch")
	}
}

// ============================================================================
// 9. 并发
// ============================================================================

func TestConcurrent_ResolveReturnsSameInstance(t *testing.T) {
	c := New()
	var cnt atomic.Int32
	D[*sess](c, func() *sess {
		cnt.Add(1)
		return &sess{id: 1}
	})
	D[*daoT](c, mkDao)
	D[*repoT](c, mkRepo)
	D[*otherD](c, mkOther)
	D[*svcT](c, mkSvc)

	const n = 200
	var wg sync.WaitGroup
	wg.Add(n)
	results := make([]*svcT, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i] = MustResolve[*svcT](c)
		}()
	}
	wg.Wait()

	first := results[0]
	for i, r := range results {
		if r != first {
			t.Errorf("results[%d] differs from results[0]", i)
			break
		}
	}
	if got := cnt.Load(); got != 1 {
		t.Errorf("factory called %d times under concurrency, want 1", got)
	}
}

func TestConcurrent_DiamondSharedAcrossGoroutines(t *testing.T) {
	var cnt int32
	c := New()
	D[*diaBottom](c, func() *diaBottom {
		atomic.AddInt32(&cnt, 1)
		return &diaBottom{v: 1}
	})
	D[*diaLeft](c, func(b *diaBottom) *diaLeft { return &diaLeft{b: b} })
	D[*diaRight](c, func(b *diaBottom) *diaRight { return &diaRight{b: b} })
	D[*diaTop](c, func(l *diaLeft, r *diaRight) *diaTop {
		return &diaTop{l: l, r: r}
	})

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			top := MustResolve[*diaTop](c)
			if top.l.b != top.r.b {
				t.Error("diamond broken under concurrency")
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&cnt); got != 1 {
		t.Errorf("bottom constructed %d times, want 1", got)
	}
}

// ============================================================================
// 10. 表驱动：错误信息稳定性
// ============================================================================

func TestErrorMessages_AreStable(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(c *Container)
		action    func(c *Container) error
		contains  []string
		notHas    []string
		wantIsErr error
	}{
		{
			name:     "NotFound for empty",
			setup:    func(c *Container) {},
			action:   func(c *Container) error { _, err := Resolve[*sess](c); return err },
			contains: []string{"*di.sess"},
		},
		{
			name: "NotFound deep in chain",
			setup: func(c *Container) {
				D[*sess](c, mkSess(1))
				D[*daoT](c, mkDao)
			},
			action: func(c *Container) error {
				_, err := Resolve[*svcT](c)
				return err
			},
			contains: []string{"*di.svcT"},
		},
		{
			name: "Factory error wrapped",
			setup: func(c *Container) {
				D[*sess](c, func() (*sess, error) { return nil, fmt.Errorf("inner") })
				D[*daoT](c, mkDao)
			},
			action: func(c *Container) error {
				_, err := Resolve[*daoT](c)
				return err
			},
			contains: []string{"inner", "parameter 0", "*di.sess"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New()
			tc.setup(c)
			err := tc.action(c)
			if err == nil {
				t.Fatal("expected error")
			}
			for _, s := range tc.contains {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("err = %q, want contains %q", err.Error(), s)
				}
			}
		})
	}
}

// ============================================================================
// 11. Benchmark
// ============================================================================

func BenchmarkResolve_SingletonHit(b *testing.B) {
	c := New()
	D[*sess](c, mkSess(1))
	D[*daoT](c, mkDao)
	D[*repoT](c, mkRepo)
	D[*otherD](c, mkOther)
	D[*svcT](c, mkSvc)
	_ = MustResolve[*svcT](c) // warm cache

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = MustResolve[*svcT](c)
	}
}

func BenchmarkResolve_DeepChain(b *testing.B) {
	type l0 struct{}
	type l1 struct{ a *l0 }
	type l2 struct{ a *l1 }
	type l3 struct{ a *l2 }
	type l4 struct{ a *l3 }
	type l5 struct{ a *l4 }

	c := New()
	D[*l0](c, func() *l0 { return &l0{} })
	D[*l1](c, func(a *l0) *l1 { return &l1{a: a} })
	D[*l2](c, func(a *l1) *l2 { return &l2{a: a} })
	D[*l3](c, func(a *l2) *l3 { return &l3{a: a} })
	D[*l4](c, func(a *l3) *l4 { return &l4{a: a} })
	_ = D[*l5](c, func(a *l4) *l5 { return &l5{a: a} })
	_ = MustResolve[*l5](c) // warm

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = MustResolve[*l5](c)
	}
}

// ============================================================================
// helpers
// ============================================================================

func expectPanic(t *testing.T, msg string) {
	t.Helper()
	if r := recover(); r == nil {
		t.Fatal(msg)
	}
}
