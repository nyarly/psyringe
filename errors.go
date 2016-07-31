package psyringe

import (
	"fmt"
	"reflect"
)

// NoConstructorOrValue is an error returned when Psyringe has no way of
// getting a value of a specific type when attempting to invoke another of its
// constructors that has a parameter of that type.
type NoConstructorOrValue struct {
	// ForType is the type for which no constructor or value is available.
	ForType reflect.Type
	// ConstructorType is the type of the constructor function requiring a
	// value of type ForType.
	ConstructorType reflect.Type
	// ConstructorParamIndex is the zero-based index of the first parameter
	// in ConstructorType of type ForType.
	ConstructorParamIndex int
}

func (e NoConstructorOrValue) Error() string {
	return fmt.Sprintf("injection type %s not known (calling constructor %s)",
		e.ForType, e.ConstructorType)
}
