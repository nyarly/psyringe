/*
Package psyringe provides an easy to use, lazy and concurrent dependency
injector.

Psyringe makes dependency injection very easy for well-written Go code. It
uses Go's type system to decide what to inject, and uses channels to orchestrate
value construction, automatically being as concurrent as your dependency graph
allows.

Psyringe does not rely on messy struct field tags nor verbose graph construction
syntax, and it keeps "magic" to an absolute minimum. It is very flexible
and has a small interface, allowing you to tailor things like scopes and object
lifetimes very easily using standard Go code.

The examples (below) should speak for themselves, but if you want a deeper
explanation of how Psyringe works, read on.

Injection Type

Values and constructors added to psyringe have an implicit "injection type".
This is the type of value that constructor or value represents in the graph. For
non-constructor values, the injection type is the type of the value itself,
determined by reflect.GetType(). For constructors, it is the type of the first
output (return) value. It is important to understand this concept, since a
single psyringe can have only one value or constructor per injection type.
`Add` will return an error if you try to register multiple values and/or
constructors that have the same injection type.

Constructors

Go does not have an explicit concept of "constructor". In Psyringe, constructors
are defined as any function that returns either a single value, or two values
where the second is an error. They can have any number of input parameters.

How Injection Works

A Psyringe knows how to populate fields in a struct with values of any injection
type that has been added to it.

When called upon to generate a value, via a call to Inject, the Psyringe
implicitly constructs a directed acyclic graph (DAG) from the constructors and
values, channelling values of each injection type into the relevant parameter
of any constructors which require it, and ultimately into any fields of that
type in the target struct which require it.

For a given Psyringe, each constructor will be called at most once. After that,
the generated value is provided directly without calling the constructor again.
Thus every value in a Psyringe is effectively a singleton. The Clone method
allows taking snapshots of a Psyringe in order to re-use its constructor graph
whilst generating new values. It is idiomatic to use multiple Psyringes with
differing scopes to inject different fields into the same object.
*/
package psyringe

import (
	"fmt"
	"reflect"
	"sync"
)

// Psyringe is a dependency injection container.
type Psyringe interface {
	Add(constructorsAndValues ...interface{})
	AddErr(constructorsAndValues ...interface{}) error
	Inject(targetStructPointers ...interface{}) error
	MustInject(targetStructPointers ...interface{})
	Clone() Psyringe
	Test() error
	SetDebugFunc(func(...interface{}))
}

type psy struct {
	values         map[reflect.Type]reflect.Value
	ctors          map[reflect.Type]*ctor
	injectionTypes map[reflect.Type]struct{}
	debug          func(...interface{})
}

var (
	terror    = reflect.TypeOf((*error)(nil)).Elem()
	noopDebug = func(...interface{}) {}
)

// New creates a new Psyringe, and adds the provided constructors and values to
// it. New will panic if any two arguments have the same injection type. Ssee
// package level documentation for definition of "injection type".
func New(constructorsAndValues ...interface{}) Psyringe {
	p, err := NewErr(constructorsAndValues...)
	if err != nil {
		panic(err)
	}
	return p
}

// NewErr is similar to New, but returns an error instead of panicking. This is
// useful if you are dynamically generating the arguments.
func NewErr(constructorsAndValues ...interface{}) (Psyringe, error) {
	p := &psy{
		values:         map[reflect.Type]reflect.Value{},
		ctors:          map[reflect.Type]*ctor{},
		injectionTypes: map[reflect.Type]struct{}{},
		debug:          noopDebug,
	}
	return p, p.AddErr(constructorsAndValues...)
}

// Add adds constructors and values to the Psyringe. It panics if any
// pair of constructors and values have the same injection type. See package
// documentation for definition of "injection type".
//
// Add uses reflection to determine whether each passed value is a constructor
// or not. For each constructor, it then generates a generic function in terms
// of reflect.Values ready to be used by a call to Inject. As such, Add is a
// relatively expensive call. See Clone for how to avoid calling Add too often.
func (p *psy) Add(constructorsAndValues ...interface{}) {
	if err := p.AddErr(constructorsAndValues...); err != nil {
		panic(err)
	}
}

// AddErr is similar to Add, but returns an error instead of panicking. This is
// useful if you are dynamically generating the arguments.
func (p *psy) AddErr(constructorsAndValues ...interface{}) error {
	for i, thing := range constructorsAndValues {
		if thing == nil {
			return fmt.Errorf("cannot add nil (argument %d)", i)
		}
		if err := p.add(thing); err != nil {
			return err
		}
	}
	return nil
}

// Clone returns a clone of this Psyringe.
//
// Clone exists to provide efficiency by allowing you to Add constructors and
// values once, and then invoke them multiple times for different instances.
// This is especially important in long-running applications where the cost of
// calling Add or New repeatedly may get expensive.
func (p *psy) Clone() Psyringe {
	q := *p
	q.ctors = map[reflect.Type]*ctor{}
	q.values = map[reflect.Type]reflect.Value{}
	for t, c := range p.ctors {
		q.ctors[t] = c.clone()
	}
	for t, v := range p.values {
		q.values[t] = v
	}
	return &q
}

// SetDebugFunc allows you to pass a debug function which will be sent debug
// level logs. The debug function has the same signature as log.Println from the
// standard library.
//
// If you do not call SetDebugFunc, or if you pass it nil, debug messages will be
// ignored.
func (p *psy) SetDebugFunc(f func(...interface{})) {
	if f != nil {
		p.debug = f
	} else {
		p.debug = noopDebug
	}
}

// Inject takes a list of targets, which must be pointers to structs. It
// tries to inject a value for each field in each target, if a value is known
// for that field's type. All targets, and all fields in each target, are
// resolved concurrently where the graph allows. In the instance that the
// Psyringe knows no injection type for a given field's type, that field is
// passed over, leaving it with whatever value it already had.
//
// See package documentation for details on how a Psyringe injects values.
func (p *psy) Inject(targets ...interface{}) error {
	wg := sync.WaitGroup{}
	wg.Add(len(targets))
	errs := make(chan error)
	go func() {
		wg.Wait()
		close(errs)
	}()
	for _, t := range targets {
		go func(target interface{}) {
			defer wg.Done()
			if err := p.inject(target); err != nil {
				p.debug("error injecting into %T: %s", target, err)
				errs <- err
			}
			p.debug("finished injecting into %T", target)
		}(t)
	}
	return <-errs
}

// MustInject wraps Inject and panics if Inject returns an error.
func (p *psy) MustInject(targets ...interface{}) {
	if err := p.Inject(targets...); err != nil {
		panic(err)
	}
}

// Test checks that all constructors' parameters are satisfied within this
// Psyringe. This method can be used in your own tests to ensure you have a
// complete graph.
func (p *psy) Test() error {
	for _, c := range p.ctors {
		if err := c.testParametersAreRegisteredIn(p); err != nil {
			return err
		}
	}
	return nil
}

// inject just tries to inject a value for each field in target, no errors if it
// doesn't know how to inject a value for a given field's type, those fields are
// just left as-is.
func (p *psy) inject(target interface{}) error {
	v := reflect.ValueOf(target)
	ptr := v.Type()
	if ptr.Kind() != reflect.Ptr {
		return fmt.Errorf("got a %s; want a pointer", ptr)
	}
	t := ptr.Elem()
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("got a %s, but %s is not a struct", ptr, t)
	}
	if v.IsNil() {
		return fmt.Errorf("got a %s, but it was nil", ptr)
	}
	nfs := t.NumField()
	wg := sync.WaitGroup{}
	wg.Add(nfs)
	errs := make(chan error)
	go func() {
		wg.Wait()
		close(errs)
	}()
	for i := 0; i < nfs; i++ {
		go func(f reflect.Value, fieldName string) {
			defer wg.Done()
			if fv, ok, err := p.getValueForStructField(f.Type()); ok && err == nil {
				f.Set(fv)
				p.debug("populated %s.%s with %v", t, fieldName, fv)
			} else if err != nil {
				errs <- err
			}
		}(v.Elem().Field(i), t.Field(i).Name)
	}
	return <-errs
}

func (p *psy) add(thing interface{}) error {
	v := reflect.ValueOf(thing)
	t := v.Type()
	var err error
	var what string
	if c := newCtor(t, v); c != nil {
		what = "constructor for " + c.outType.Name()
		err = p.addCtor(c)
	} else {
		what = "fully realised value " + fmt.Sprint(thing)
		err = p.addValue(t, v)
	}
	if err != nil {
		p.debug("error adding %s (%T): %s", what, thing, err)
	} else {
		p.debug("added %s (%T)", what, thing)
	}
	return err
}

func (p *psy) getValueForStructField(t reflect.Type) (reflect.Value, bool, error) {
	if v, ok := p.values[t]; ok {
		return v, true, nil
	}
	c, ok := p.ctors[t]
	if !ok {
		return reflect.Value{}, false, nil
	}
	v, err := c.getValue(p)
	return v, true, err
}

func (p *psy) getValueForConstructor(forCtor *ctor, paramIndex int, t reflect.Type) (reflect.Value, error) {
	if v, ok := p.values[t]; ok {
		return v, nil
	}
	c, ok := p.ctors[t]
	if !ok {
		return reflect.Value{}, NoConstructorOrValue{
			ConstructorType:       forCtor.funcType,
			ConstructorParamIndex: paramIndex,
			ForType:               t,
		}
	}
	return c.getValue(p)
}

func (p *psy) addCtor(c *ctor) error {
	p.ctors[c.outType] = c
	return p.registerInjectionType(c.outType)
}

func (p *psy) addValue(t reflect.Type, v reflect.Value) error {
	p.values[t] = v
	return p.registerInjectionType(t)
}

func (p *psy) registerInjectionType(t reflect.Type) error {
	if _, alreadyRegistered := p.injectionTypes[t]; alreadyRegistered {
		return fmt.Errorf("injection type %s already registered", t)
	}
	p.injectionTypes[t] = struct{}{}
	return nil
}
