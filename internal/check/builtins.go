package check

import (
	"sort"
	"strings"
)

// A builtin is a method of a built-in type. Its key names it for the emitter.
// Type arguments are the receiver's (List<T>: T; Option<T>: T; Result<T, E>:
// T, E) followed by the method's own.
type builtin struct {
	key  string
	own  int    // the method's own type parameters
	warn string // W0002 message for unwrap and expect
	elem string // what a list method needs of its elements: comparable, ordered, number or string
	sig  func(r, o []Type) ([]Type, Type)
}

func fn1(param, result Type) Type { return &Func{[]Type{param}, result} }

var listMethods = map[string]*builtin{
	"len":  {key: "list.len", sig: func(r, o []Type) ([]Type, Type) { return nil, Int }},
	"get":  {key: "list.get", sig: func(r, o []Type) ([]Type, Type) { return []Type{Int}, &Option{r[0]} }},
	"push": {key: "list.push", sig: func(r, o []Type) ([]Type, Type) { return []Type{r[0]}, &List{r[0]} }},
	"map": {key: "list.map", own: 1, sig: func(r, o []Type) ([]Type, Type) {
		return []Type{fn1(r[0], o[0])}, &List{o[0]}
	}},
	"filter": {key: "list.filter", sig: func(r, o []Type) ([]Type, Type) {
		return []Type{fn1(r[0], Bool)}, &List{r[0]}
	}},
	"fold": {key: "list.fold", own: 1, sig: func(r, o []Type) ([]Type, Type) {
		return []Type{o[0], &Func{[]Type{o[0], r[0]}, o[0]}}, o[0]
	}},
	"is_empty": {key: "list.is_empty", sig: func(r, o []Type) ([]Type, Type) { return nil, Bool }},
	"first":    {key: "list.first", sig: func(r, o []Type) ([]Type, Type) { return nil, &Option{r[0]} }},
	"last":     {key: "list.last", sig: func(r, o []Type) ([]Type, Type) { return nil, &Option{r[0]} }},
	"set":      {key: "list.set", sig: func(r, o []Type) ([]Type, Type) { return []Type{Int, r[0]}, &List{r[0]} }},
	"concat":   {key: "list.concat", sig: func(r, o []Type) ([]Type, Type) { return []Type{&List{r[0]}}, &List{r[0]} }},
	"slice":    {key: "list.slice", sig: func(r, o []Type) ([]Type, Type) { return []Type{Int, Int}, &List{r[0]} }},
	"reverse":  {key: "list.reverse", sig: func(r, o []Type) ([]Type, Type) { return nil, &List{r[0]} }},
	"any":      {key: "list.any", sig: func(r, o []Type) ([]Type, Type) { return []Type{fn1(r[0], Bool)}, Bool }},
	"all":      {key: "list.all", sig: func(r, o []Type) ([]Type, Type) { return []Type{fn1(r[0], Bool)}, Bool }},
	"find":     {key: "list.find", sig: func(r, o []Type) ([]Type, Type) { return []Type{fn1(r[0], Bool)}, &Option{r[0]} }},
	"enumerate": {key: "list.enumerate", sig: func(r, o []Type) ([]Type, Type) {
		return nil, &List{&Tuple{[]Type{Int, r[0]}}}
	}},
	"contains": {key: "list.contains", elem: "comparable", sig: func(r, o []Type) ([]Type, Type) { return []Type{r[0]}, Bool }},
	"index_of": {key: "list.index_of", elem: "comparable", sig: func(r, o []Type) ([]Type, Type) {
		return []Type{r[0]}, &Option{Int}
	}},
	"sort": {key: "list.sort", elem: "ordered", sig: func(r, o []Type) ([]Type, Type) { return nil, &List{r[0]} }},
	"min":  {key: "list.min", elem: "ordered", sig: func(r, o []Type) ([]Type, Type) { return nil, &Option{r[0]} }},
	"max":  {key: "list.max", elem: "ordered", sig: func(r, o []Type) ([]Type, Type) { return nil, &Option{r[0]} }},
	"sum":  {key: "list.sum", elem: "number", sig: func(r, o []Type) ([]Type, Type) { return nil, r[0] }},
	"join": {key: "list.join", elem: "string", sig: func(r, o []Type) ([]Type, Type) { return []Type{String}, String }},
}

var mapMethods = map[string]*builtin{
	"len":      {key: "map.len", sig: func(r, o []Type) ([]Type, Type) { return nil, Int }},
	"is_empty": {key: "map.is_empty", sig: func(r, o []Type) ([]Type, Type) { return nil, Bool }},
	"get":      {key: "map.get", sig: func(r, o []Type) ([]Type, Type) { return []Type{r[0]}, &Option{r[1]} }},
	"contains_key": {key: "map.contains_key", sig: func(r, o []Type) ([]Type, Type) {
		return []Type{r[0]}, Bool
	}},
	"insert": {key: "map.insert", sig: func(r, o []Type) ([]Type, Type) {
		return []Type{r[0], r[1]}, &Map{r[0], r[1]}
	}},
	"remove": {key: "map.remove", sig: func(r, o []Type) ([]Type, Type) { return []Type{r[0]}, &Map{r[0], r[1]} }},
	"keys":   {key: "map.keys", sig: func(r, o []Type) ([]Type, Type) { return nil, &List{r[0]} }},
	"values": {key: "map.values", sig: func(r, o []Type) ([]Type, Type) { return nil, &List{r[1]} }},
	"entries": {key: "map.entries", sig: func(r, o []Type) ([]Type, Type) {
		return nil, &List{&Tuple{[]Type{r[0], r[1]}}}
	}},
}

var optionMethods = map[string]*builtin{
	"is_some":   {key: "option.is_some", sig: func(r, o []Type) ([]Type, Type) { return nil, Bool }},
	"is_none":   {key: "option.is_none", sig: func(r, o []Type) ([]Type, Type) { return nil, Bool }},
	"unwrap_or": {key: "option.unwrap_or", sig: func(r, o []Type) ([]Type, Type) { return []Type{r[0]}, r[0] }},
	"map": {key: "option.map", own: 1, sig: func(r, o []Type) ([]Type, Type) {
		return []Type{fn1(r[0], o[0])}, &Option{o[0]}
	}},
	"and_then": {key: "option.and_then", own: 1, sig: func(r, o []Type) ([]Type, Type) {
		return []Type{fn1(r[0], &Option{o[0]})}, &Option{o[0]}
	}},
	"ok_or": {key: "option.ok_or", own: 1, sig: func(r, o []Type) ([]Type, Type) {
		return []Type{o[0]}, &Result{r[0], o[0]}
	}},
	"unwrap": {key: "option.unwrap", warn: "`unwrap` panics when the Option is None; handle None with match or ?",
		sig: func(r, o []Type) ([]Type, Type) { return nil, r[0] }},
	"expect": {key: "option.expect", warn: "`expect` panics when the Option is None; handle None with match or ?",
		sig: func(r, o []Type) ([]Type, Type) { return []Type{String}, r[0] }},
}

var resultMethods = map[string]*builtin{
	"is_ok":     {key: "result.is_ok", sig: func(r, o []Type) ([]Type, Type) { return nil, Bool }},
	"is_err":    {key: "result.is_err", sig: func(r, o []Type) ([]Type, Type) { return nil, Bool }},
	"unwrap_or": {key: "result.unwrap_or", sig: func(r, o []Type) ([]Type, Type) { return []Type{r[0]}, r[0] }},
	"map": {key: "result.map", own: 1, sig: func(r, o []Type) ([]Type, Type) {
		return []Type{fn1(r[0], o[0])}, &Result{o[0], r[1]}
	}},
	"map_err": {key: "result.map_err", own: 1, sig: func(r, o []Type) ([]Type, Type) {
		return []Type{fn1(r[1], o[0])}, &Result{r[0], o[0]}
	}},
	"and_then": {key: "result.and_then", own: 1, sig: func(r, o []Type) ([]Type, Type) {
		return []Type{fn1(r[0], &Result{o[0], r[1]})}, &Result{o[0], r[1]}
	}},
	"ok": {key: "result.ok", sig: func(r, o []Type) ([]Type, Type) { return nil, &Option{r[0]} }},
	"unwrap": {key: "result.unwrap", warn: "`unwrap` panics when the Result is an Err; handle the error with match or ?",
		sig: func(r, o []Type) ([]Type, Type) { return nil, r[0] }},
	"expect": {key: "result.expect", warn: "`expect` panics when the Result is an Err; handle the error with match or ?",
		sig: func(r, o []Type) ([]Type, Type) { return []Type{String}, r[0] }},
}

var errorMethods = map[string]*builtin{
	"message": {key: "error.message", sig: func(r, o []Type) ([]Type, Type) { return nil, String }},
	"context": {key: "error.context", sig: func(r, o []Type) ([]Type, Type) { return []Type{String}, ErrorT }},
}

func toString(key string) map[string]*builtin {
	return map[string]*builtin{"to_string": {key: key, sig: func(r, o []Type) ([]Type, Type) { return nil, String }}}
}

// simple returns a method of a primitive type with the given parameter and
// result types.
func simple(key string, result Type, params ...Type) *builtin {
	return &builtin{key: key, sig: func(r, o []Type) ([]Type, Type) { return params, result }}
}

// with adds methods to a method table.
func with(m map[string]*builtin, more map[string]*builtin) map[string]*builtin {
	for k, v := range more {
		m[k] = v
	}
	return m
}

var basicMethods = map[*Basic]map[string]*builtin{
	Int: with(toString("int.to_string"), map[string]*builtin{
		"abs": simple("int.abs", Int),
		"min": simple("int.min", Int, Int),
		"max": simple("int.max", Int, Int),
	}),
	Float: with(toString("float.to_string"), map[string]*builtin{
		"abs":   simple("float.abs", Float),
		"floor": simple("float.floor", Float),
		"ceil":  simple("float.ceil", Float),
		"round": simple("float.round", Float),
		"sqrt":  simple("float.sqrt", Float),
	}),
	Bool: toString("bool.to_string"),
	Byte: toString("byte.to_string"),
	String: {
		"to_string":   simple("string.to_string", String),
		"parse_int":   simple("string.parse_int", &Result{Int, ErrorT}),
		"parse_float": simple("string.parse_float", &Result{Float, ErrorT}),
		"len":         simple("string.len", Int),
		"is_empty":    simple("string.is_empty", Bool),
		"contains":    simple("string.contains", Bool, String),
		"starts_with": simple("string.starts_with", Bool, String),
		"ends_with":   simple("string.ends_with", Bool, String),
		"find":        simple("string.find", &Option{Int}, String),
		"split":       simple("string.split", &List{String}, String),
		"lines":       simple("string.lines", &List{String}),
		"trim":        simple("string.trim", String),
		"to_upper":    simple("string.to_upper", String),
		"to_lower":    simple("string.to_lower", String),
		"replace":     simple("string.replace", String, String, String),
		"repeat":      simple("string.repeat", String, Int),
		"slice":       simple("string.slice", String, Int, Int),
	},
	ErrorT: errorMethods,
}

// The capabilities' methods (DESIGN §5.2).
var (
	outMethods = map[string]*builtin{
		"print":   simple("out.print", Unit, String),
		"println": simple("out.println", Unit, String),
	}
	envMethods = map[string]*builtin{
		"get":  simple("env.get", &Option{String}, String),
		"args": simple("env.args", &List{String}),
	}
	fsMethods = map[string]*builtin{
		"read":   simple("fs.read", &Result{String, ErrorT}, String),
		"write":  simple("fs.write", &Result{Unit, ErrorT}, String, String),
		"exists": simple("fs.exists", Bool, String),
		"list":   simple("fs.list", &Result{&List{String}, ErrorT}, String),
	}
	clockMethods = map[string]*builtin{
		"now_ms": simple("clock.now_ms", Int),
	}
	capMethods = map[*Cap]map[string]*builtin{OutT: outMethods, EnvT: envMethods, FsT: fsMethods, ClockT: clockMethods}
)

// methodList names the methods of a table, in order.
func methodList(m map[string]*builtin) string {
	var names []string
	for n := range m {
		names = append(names, "`"+n+"`")
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// methodsOf returns the built-in methods of t, the receiver's type arguments
// and the name used in messages.
func methodsOf(t Type) (map[string]*builtin, []Type, string) {
	switch t := t.(type) {
	case *List:
		return listMethods, []Type{t.Elem}, "List"
	case *Map:
		return mapMethods, []Type{t.Key, t.Val}, "Map"
	case *Option:
		return optionMethods, []Type{t.Elem}, "Option"
	case *Result:
		return resultMethods, []Type{t.Ok, t.Err}, "Result"
	case *Basic:
		return basicMethods[t], nil, t.name
	case *Cap:
		return capMethods[t], nil, t.Name
	case *Tuple:
		return nil, nil, "a tuple"
	case *Func:
		return nil, nil, "a function"
	}
	return nil, nil, t.String()
}
