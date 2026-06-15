// Package di 提供一个轻量的依赖注入容器：按类型注册工厂，解析时自动
// 注入形参，并以单例形式缓存结果。
package di

import (
	"fmt"
	"reflect"
	"sync"
)

// -----------------------------------------------------------------------------
// 错误类型
// -----------------------------------------------------------------------------

// NotFoundError 表示容器中没有注册某个类型对应的工厂。可以用 errors.As 捕获。
type NotFoundError struct {
	Type reflect.Type
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("di: no factory registered for type %v", e.Type)
}

// -----------------------------------------------------------------------------
// Container
// -----------------------------------------------------------------------------

// Container 持有「类型 -> 工厂」的映射，并缓存已解析的实例（单例）。
type Container struct {
	mu        sync.RWMutex
	factories map[reflect.Type]factoryFn
	cache     map[reflect.Type]any
	inflight  map[reflect.Type]chan struct{} // 正在构造中的类型 -> 完成信号
}

// factoryFn 是内部用的工厂签名：拿到容器，返回 any + error。
type factoryFn func(c *Container) (any, error)

// New 创建一个空的容器。
func New() *Container {
	return &Container{
		factories: make(map[reflect.Type]factoryFn),
		cache:     make(map[reflect.Type]any),
		inflight:  make(map[reflect.Type]chan struct{}),
	}
}

// 注册一个工厂到 t 对应的类型上。重复注册会 panic。
func (c *Container) register(t reflect.Type, factory factoryFn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.factories[t]; exists {
		panic(fmt.Sprintf("di: type %v already registered", t))
	}
	c.factories[t] = factory
}

// resolve 内部按类型拿到一个实例（单例）。
//
// 并发安全策略：
//   - 缓存命中走 RLock，快速返回。
//   - 缓存未命中：上写锁，要么自己拿到构造权，要么发现有人正在构造（inflight），
//     则等待其完成信号，再 double-check 缓存。
//   - inflight 机制保证同一类型的高并发 miss 不会重复调用工厂。
func (c *Container) resolve(t reflect.Type) (any, error) {
	// 1) 快速路径：缓存命中。
	c.mu.RLock()
	if v, ok := c.cache[t]; ok {
		c.mu.RUnlock()
		return v, nil
	}
	c.mu.RUnlock()

	// 2) 慢速路径：拿写锁争抢构造权 / 等待别人。
	c.mu.Lock()
	if v, ok := c.cache[t]; ok { // double-check
		c.mu.Unlock()
		return v, nil
	}
	if ch, ok := c.inflight[t]; ok {
		c.mu.Unlock()
		<-ch
		// 别人构造完了，再检查一次缓存。
		c.mu.RLock()
		v, ok := c.cache[t]
		c.mu.RUnlock()
		if ok {
			return v, nil
		}
		return nil, fmt.Errorf("di: %v resolved concurrently but not cached", t)
	}
	factory, ok := c.factories[t]
	if !ok {
		c.mu.Unlock()
		return nil, &NotFoundError{Type: t}
	}
	ch := make(chan struct{})
	c.inflight[t] = ch
	c.mu.Unlock()

	// 3) 真正调用工厂（持锁外执行，避免递归死锁）。
	v, err := factory(c)

	// 4) 不管成功失败，都要清理 inflight 并通知等待者。
	c.mu.Lock()
	delete(c.inflight, t)
	close(ch)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	// typed-nil 检查：接口里有类型但指针为 nil 也要报错。
	if v == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("di: factory for %v returned nil", t)
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Ptr && rv.IsNil() {
		c.mu.Unlock()
		return nil, fmt.Errorf("di: factory for %v returned typed nil", t)
	}
	// 再 double-check 一次：如果并发场景下已被别人先缓存，沿用旧值。
	if existing, ok := c.cache[t]; ok {
		c.mu.Unlock()
		return existing, nil
	}
	c.cache[t] = v
	c.mu.Unlock()
	return v, nil
}

// resolveArgs 按 fnType 的形参列表，依次从容器中解析每个参数。
func (c *Container) resolveArgs(fnType reflect.Type) ([]reflect.Value, error) {
	args := make([]reflect.Value, fnType.NumIn())
	for i := 0; i < fnType.NumIn(); i++ {
		paramType := fnType.In(i)
		v, err := c.resolve(paramType)
		if err != nil {
			return nil, fmt.Errorf("di: parameter %d (%v): %w", i, paramType, err)
		}
		args[i] = reflect.ValueOf(v)
	}
	return args, nil
}

// -----------------------------------------------------------------------------
// Depends / Dep
// -----------------------------------------------------------------------------

// Dep 是一个可解析的依赖句柄。通过 Depends() 拿到，调用 Get/MustGet 即可拿到值。
type Dep[T any] struct {
	c *Container
	t reflect.Type
}

// Get 解析该依赖并返回值；解析过程会按需触发整条依赖链。
func (d *Dep[T]) Get() (T, error) {
	var zero T
	v, err := d.c.resolve(d.t)
	if err != nil {
		return zero, err
	}
	out, ok := v.(T)
	if !ok {
		return zero, fmt.Errorf("di: cached value %T cannot be asserted to %v", v, d.t)
	}
	return out, nil
}

// MustGet 是 Get 的 panic 版本，常见于启动期装配。
func (d *Dep[T]) MustGet() T {
	v, err := d.Get()
	if err != nil {
		panic(err)
	}
	return v
}

// Container 返回该依赖所属的容器，便于链式组合。
func (d *Dep[T]) Container() *Container { return d.c }

// D 把工厂 fn 注册为类型 T 的提供者，并返回一个 *Dep[T] 句柄。
//
// fn 的参数类型会自动从 c 中解析（顺序无关、解析是惰性的）。
// fn 的第一个返回值必须可断言为 T，否则会 panic。fn 的返回值支持：
//   - 仅 T
//   - (T, error)
func D[T any](c *Container, fn any) *Dep[T] {
	var zero T
	t := reflect.TypeOf(zero)
	if t == nil {
		panic(fmt.Sprintf("di.Depends: cannot infer type for %T", zero))
	}
	if c == nil {
		panic("di.Depends: container is nil")
	}
	if fn == nil {
		panic("di.Depends: fn is nil")
	}

	fnVal := reflect.ValueOf(fn)
	fnType := fnVal.Type()
	if fnType.Kind() != reflect.Func {
		panic(fmt.Sprintf("di.Depends: fn must be a function, got %T", fn))
	}

	factory := func(c *Container) (any, error) {
		resolved, err := c.resolveArgs(fnType)
		if err != nil {
			return nil, err
		}
		out := fnVal.Call(resolved)
		return interpretResults[T](t, out)
	}

	c.register(t, factory)
	return &Dep[T]{c: c, t: t}
}

// interpretResults 把反射调用结果归一为 (T, error)。
func interpretResults[T any](t reflect.Type, out []reflect.Value) (any, error) {
	var zero T
	switch len(out) {
	case 0:
		return zero, nil
	case 1:
		v, ok := out[0].Interface().(T)
		if !ok {
			return nil, fmt.Errorf(
				"di.Depends[%v]: factory returned %v, cannot assert to %v",
				t, out[0].Type(), t,
			)
		}
		return v, nil
	case 2:
		if e, _ := out[1].Interface().(error); e != nil {
			return nil, e
		}
		v, ok := out[0].Interface().(T)
		if !ok {
			return nil, fmt.Errorf(
				"di.Depends[%v]: factory returned %v, cannot assert to %v",
				t, out[0].Type(), t,
			)
		}
		return v, nil
	default:
		return nil, fmt.Errorf("di.Depends[%v]: unsupported number of return values: %d", t, len(out))
	}
}

// -----------------------------------------------------------------------------
// 便捷全局解析（不返回 Dep 时使用）
// -----------------------------------------------------------------------------

// Resolve 按类型 T 取一个实例（不走 Dep 句柄的便捷写法）。
func Resolve[T any](c *Container) (T, error) {
	var zero T
	t := reflect.TypeOf(zero)
	if t == nil {
		return zero, fmt.Errorf("di.Resolve: cannot infer type for %T", zero)
	}
	v, err := c.resolve(t)
	if err != nil {
		return zero, err
	}
	out, ok := v.(T)
	if !ok {
		return zero, fmt.Errorf("di.Resolve: cached value %T cannot be asserted to %v", v, t)
	}
	return out, nil
}

// MustResolve 是 Resolve 的 panic 版本。
func MustResolve[T any](c *Container) T {
	v, err := Resolve[T](c)
	if err != nil {
		panic(err)
	}
	return v
}
