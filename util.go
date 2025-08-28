package http

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"

	rutil "go.unistack.org/micro/v4/util/reflect"
)

var (
	templateCache = make(map[string][]string)
	mu            sync.RWMutex
)

// Error struct holds error
type Error struct {
	err interface{}
}

// Error func for error interface
func (err *Error) Error() string {
	return fmt.Sprintf("%v", err.err)
}

func GetError(err error) interface{} {
	if rerr, ok := err.(*Error); ok {
		return rerr.err
	}
	return err
}

func newPathRequest(path string, method string, body string, msg interface{}, tags []string, parameters map[string]map[string]string) (string, interface{}, error) {
	// parse via https://github.com/googleapis/googleapis/blob/master/google/api/http.proto definition
	tpl, err := newTemplate(path)
	if err != nil {
		return "", nil, err
	}

	if len(tpl) > 0 && msg == nil {
		return "", nil, fmt.Errorf("nil message but path params requested: %v", path)
	}

	fieldsmapskip := make(map[string]struct{})
	fieldsmap := make(map[string]string, len(tpl))
	for _, v := range tpl {
		var vs, ve int
		for i := 0; i < len(v); i++ {
			switch v[i] {
			case '{':
				vs = i + 1
			case '}':
				ve = i
			}
			if ve != 0 {
				fieldsmap[v[vs:ve]] = ""
				vs = 0
				ve = 0
			}
		}
	}

	nmsg, err := rutil.Zero(msg)
	if err != nil {
		return "", nil, err
	}

	// we cant switch on message and use proto helpers, to avoid dependency to protobuf
	tmsg := reflect.ValueOf(msg)
	if tmsg.Kind() == reflect.Ptr {
		tmsg = tmsg.Elem()
	}

	tnmsg := reflect.ValueOf(nmsg)
	if tnmsg.Kind() == reflect.Ptr {
		tnmsg = tnmsg.Elem()
	}

	values := url.Values{}
	// copy cycle

	cleanPath := make(map[string]bool)

	var (
		bodyOverride    interface{}
		bodyOverrideSet bool
	)

	for i := 0; i < tmsg.NumField(); i++ {
		val := tmsg.Field(i)
		if val.IsZero() {
			continue
		}
		fld := tmsg.Type().Field(i)
		// Skip unexported fields.
		if !fld.IsExported() {
			continue
		}
		t := &tag{}
		for _, tn := range tags {
			ts, ok := fld.Tag.Lookup(tn)
			if !ok {
				continue
			}

			tp := strings.Split(ts, ",")
			// special
			switch tn {
			case "protobuf": // special
				for _, p := range tp {
					prefix := "json="
					if strings.HasPrefix(p, prefix) {
						t = &tag{key: tn, name: p[len(prefix):]}
						break
					}
				}
			default:
				t = &tag{key: tn, name: tp[0]}
			}

			if t.name != "" {
				break
			}
		}

		cname := t.name
		if cname == "" {
			cname = fld.Name
			// fallback to lowercase
			t.name = strings.ToLower(fld.Name)
		}
		if _, ok := parameters["header"][cname]; ok {
			continue
		}
		if _, ok := parameters["cookie"][cname]; ok {
			continue
		}

		if !val.IsValid() || val.IsZero() {
			continue
		}

		// nolint: gocritic, nestif
		if _, ok := fieldsmap[t.name]; ok {
			switch val.Type().Kind() {
			case reflect.Slice:
				for idx := 0; idx < val.Len(); idx++ {
					values.Add(t.name, getParam(val.Index(idx)))
				}
				fieldsmapskip[t.name] = struct{}{}
			default:
				fieldsmap[t.name] = getParam(val)
			}
		} else {
			for k, v := range fieldsmap {
				isSet := false
				if v != "" {
					continue
				}
				var clean []string
				fld := msg

				parts := strings.Split(k, ".")

				for idx := 0; idx < len(parts); idx++ {
					var nfld interface{}
					var name string
					if tags == nil {
						tags = []string{"json"}
					}
				tagsloop:
					for ti := 0; ti < len(tags); ti++ {
						name, nfld, err = rutil.StructFieldNameByTag(fld, tags[ti], parts[idx])
						if err == nil {
							clean = append(clean, name)
							break tagsloop
						}
					}
					if err == nil {
						fld = nfld
						if len(parts)-1 == idx {
							isSet = true
							fieldsmap[k] = fmt.Sprintf("%v", fld)

						}
					}
				}
				if isSet {
					cleanPath[strings.Join(clean, ".")] = true
				}
			}
			for k := range cleanPath {
				if err = rutil.ZeroFieldByPath(nmsg, k); err != nil {
					return "", nil, err
				}
			}
			if (body == "*" || body == t.name) && method != http.MethodGet {
				if tnmsg.Field(i).CanSet() {
					if body == t.name {
						switch val.Kind() {
						case reflect.Ptr:
							if !val.IsNil() && val.Elem().Kind() == reflect.Struct {
								bodyOverride = val.Interface()
								bodyOverrideSet = true
							} else {
								tnmsg.Field(i).Set(val)
							}
						default:
							tnmsg.Field(i).Set(val)
						}
					} else {
						tnmsg.Field(i).Set(val)
					}
				}
			} else if method == http.MethodGet {
				if val.Type().Kind() == reflect.Slice {
					for idx := 0; idx < val.Len(); idx++ {
						values.Add(t.name, getParam(val.Index(idx)))
					}
				} else if !rutil.IsEmpty(val) {
					values.Add(t.name, getParam(val))
				}
			}

		}
	}

	// check not filled stuff
	for k, v := range fieldsmap {
		_, ok := fieldsmapskip[k]
		if !ok && v == "" {
			return "", nil, fmt.Errorf("path param %s not filled", k)
		}
	}

	var b strings.Builder

	for _, fld := range tpl {
		_, _ = b.WriteRune('/')
		// nolint: nestif
		var vs, ve, vf int
		var pholder bool
		for i := 0; i < len(fld); i++ {
			switch fld[i] {
			case '{':
				vs = i + 1
			case '}':
				ve = i
			}
			// nolint: nestif
			if vs > 0 && ve != 0 {
				if vm, ok := fieldsmap[fld[vs:ve]]; ok {
					if vm != "" {
						_, _ = b.WriteString(fld[vf : vs-1])
						_, _ = b.WriteString(vm)
						vf = ve + 1
					}
				} else {
					_, _ = b.WriteString(fld)
				}
				vs = 0
				ve = 0
				pholder = true
			}
		}
		if !pholder {
			_, _ = b.WriteString(fld)
		}
	}

	if len(values) > 0 {
		_, _ = b.WriteRune('?')
		_, _ = b.WriteString(values.Encode())
	}

	if bodyOverrideSet {
		return b.String(), bodyOverride, nil
	}

	// rutil.ZeroEmpty(tnmsg.Interface())

	if rutil.IsZero(nmsg) && !isEmptyStruct(nmsg) {
		return b.String(), nil, nil
	}

	return b.String(), nmsg, nil
}

func isEmptyStruct(v interface{}) bool {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	return val.Kind() == reflect.Struct && val.NumField() == 0
}

func newTemplate(path string) ([]string, error) {
	if len(path) == 0 || path[0] != '/' {
		return nil, fmt.Errorf("path must starts with /")
	}
	mu.RLock()
	tpl, ok := templateCache[path]
	if ok {
		mu.RUnlock()
		return tpl, nil
	}
	mu.RUnlock()

	tpl = strings.Split(path[1:], "/")
	mu.Lock()
	templateCache[path] = tpl
	mu.Unlock()

	return tpl, nil
}

type tag struct {
	key  string
	name string
}

func getParam(val reflect.Value) string {
	var v string
	switch val.Kind() {
	case reflect.Ptr:
		switch reflect.Indirect(val).Type().String() {
		case
			"wrapperspb.BoolValue",
			"wrapperspb.BytesValue",
			"wrapperspb.DoubleValue",
			"wrapperspb.FloatValue",
			"wrapperspb.Int32Value", "wrapperspb.Int64Value",
			"wrapperspb.StringValue",
			"wrapperspb.UInt32Value", "wrapperspb.UInt64Value":
			if eva := reflect.Indirect(val).FieldByName("Value"); eva.IsValid() {
				v = getParam(eva)
			}
		}
	default:
		v = fmt.Sprintf("%v", val.Interface())
	}
	return v
}
