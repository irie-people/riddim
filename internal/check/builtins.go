package check

// A builtin is a method of a built-in type. Its key names it for the emitter.
// Type arguments are the receiver's (List<T>: T; Option<T>: T; Result<T, E>:
// T, E) followed by the method's own.
type builtin struct {
	key  string
	own  int    // the method's own type parameters
	warn string // W0002 message for unwrap and expect
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

var basicMethods = map[*Basic]map[string]*builtin{
	Int:   toString("int.to_string"),
	Float: toString("float.to_string"),
	Bool:  toString("bool.to_string"),
	Byte:  toString("byte.to_string"),
	String: {
		"to_string": {key: "string.to_string", sig: func(r, o []Type) ([]Type, Type) { return nil, String }},
		"parse_int": {key: "string.parse_int", sig: func(r, o []Type) ([]Type, Type) { return nil, &Result{Int, ErrorT} }},
	},
	ErrorT: errorMethods,
}

var outMethods = map[string]*builtin{
	"print":   {key: "out.print", sig: func(r, o []Type) ([]Type, Type) { return []Type{String}, Unit }},
	"println": {key: "out.println", sig: func(r, o []Type) ([]Type, Type) { return []Type{String}, Unit }},
}

// laterMethods are the §4.9 methods that come after this version, so that
// using one gets a clear message.
var laterMethods = map[string][]string{
	"String": {"len", "is_empty", "contains", "starts_with", "ends_with", "find", "split", "lines", "trim",
		"to_upper", "to_lower", "replace", "repeat", "slice", "parse_float"},
	"Int":   {"abs", "min", "max"},
	"Float": {"abs", "floor", "ceil", "round", "sqrt"},
	"List": {"is_empty", "first", "last", "set", "concat", "slice", "reverse", "any", "all", "find",
		"enumerate", "contains", "index_of", "sort", "min", "max", "sum", "join"},
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
		if t == OutT {
			return outMethods, nil, "Out"
		}
		return nil, nil, t.Name
	case *Tuple:
		return nil, nil, "a tuple"
	case *Func:
		return nil, nil, "a function"
	}
	return nil, nil, t.String()
}

func isLater(typeName, method string) bool {
	for _, m := range laterMethods[typeName] {
		if m == method {
			return true
		}
	}
	return false
}
