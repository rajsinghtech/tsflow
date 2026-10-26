package database

// Strict parsers for the JSON this server writes into node_pairs and
// node_pair_hours. They accept compact JSON only: no whitespace, no escapes,
// integers without leading zeros, fractions or exponents that fit in int64.
// A false or errStatsSinglePass means the row goes to the SQL path, which
// keeps SQLite's exact handling of everything else.

// parseJSONInt reads a canonical JSON integer at b[i:].
func parseJSONInt(b []byte, i int) (int64, int, bool) {
	neg := false
	if i < len(b) && b[i] == '-' {
		neg = true
		i++
	}
	if i >= len(b) || b[i] < '0' || b[i] > '9' {
		return 0, i, false
	}
	if b[i] == '0' {
		i++
		if neg {
			return 0, i, false // "-0"
		}
		if i < len(b) && (b[i] >= '0' && b[i] <= '9' || b[i] == '.' || b[i] == 'e' || b[i] == 'E') {
			return 0, i, false
		}
		return 0, i, true
	}
	var u uint64
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		d := uint64(b[i] - '0')
		if u > (1<<63)/10 || u*10 > (1<<63)-d {
			return 0, i, false
		}
		u = u*10 + d
		i++
	}
	if i < len(b) && (b[i] == '.' || b[i] == 'e' || b[i] == 'E') {
		return 0, i, false
	}
	if u > 1<<63-1 {
		return 0, i, false
	}
	if neg {
		return -int64(u), i, true
	}
	return int64(u), i, true
}

// parseProtocolBytes walks {"6":10,"17":5}. fn gets each key as a number,
// or isNull for the "null" key the hourly rollup keeps.
func parseProtocolBytes(b []byte, fn func(isNull bool, proto, bytes int64) bool) error {
	if len(b) < 2 || b[0] != '{' || b[len(b)-1] != '}' {
		return errStatsSinglePass
	}
	i := 1
	if b[i] == '}' {
		return nil
	}
	for {
		if i >= len(b) || b[i] != '"' {
			return errStatsSinglePass
		}
		i++
		isNull := false
		var proto int64
		if i+5 <= len(b) && string(b[i:i+5]) == `null"` {
			isNull = true
			i += 4
		} else {
			var ok bool
			proto, i, ok = parseJSONInt(b, i)
			if !ok {
				return errStatsSinglePass
			}
		}
		if i+1 >= len(b) || b[i] != '"' || b[i+1] != ':' {
			return errStatsSinglePass
		}
		bytes, next, ok := parseJSONInt(b, i+2)
		if !ok {
			return errStatsSinglePass
		}
		i = next
		if !fn(isNull, proto, bytes) {
			return errStatsSinglePass
		}
		if i >= len(b) {
			return errStatsSinglePass
		}
		if b[i] == '}' {
			if i != len(b)-1 {
				return errStatsSinglePass
			}
			return nil
		}
		if b[i] != ',' {
			return errStatsSinglePass
		}
		i++
	}
}

// parseIntArray reads [6,17] into out.
func parseIntArray(b []byte, out []int64) ([]int64, bool) {
	if len(b) < 2 || b[0] != '[' || b[len(b)-1] != ']' {
		return out, false
	}
	i := 1
	if b[i] == ']' {
		return out, true
	}
	for {
		v, next, ok := parseJSONInt(b, i)
		if !ok {
			return out, false
		}
		out = append(out, v)
		i = next
		if i >= len(b) {
			return out, false
		}
		if b[i] == ']' {
			return out, i == len(b)-1
		}
		if b[i] != ',' {
			return out, false
		}
		i++
	}
}

// parsePortList reads [{"port":443,"proto":6,"bytes":10},...]. Each object
// must have exactly those three keys, in any order.
func parsePortList(b []byte, fn func(proto, port, bytes int64) bool) error {
	if len(b) < 2 || b[0] != '[' || b[len(b)-1] != ']' {
		return errStatsSinglePass
	}
	i := 1
	if b[i] == ']' {
		return nil
	}
	for {
		if i >= len(b) || b[i] != '{' {
			return errStatsSinglePass
		}
		i++
		var vals [3]int64
		var seen [3]bool
		for {
			key := -1
			switch {
			case i+7 <= len(b) && string(b[i:i+7]) == `"port":`:
				key, i = 0, i+7
			case i+8 <= len(b) && string(b[i:i+8]) == `"proto":`:
				key, i = 1, i+8
			case i+8 <= len(b) && string(b[i:i+8]) == `"bytes":`:
				key, i = 2, i+8
			}
			if key < 0 || seen[key] {
				return errStatsSinglePass
			}
			v, next, ok := parseJSONInt(b, i)
			if !ok {
				return errStatsSinglePass
			}
			vals[key], seen[key], i = v, true, next
			if i >= len(b) {
				return errStatsSinglePass
			}
			if b[i] == '}' {
				i++
				break
			}
			if b[i] != ',' {
				return errStatsSinglePass
			}
			i++
		}
		if !seen[0] || !seen[1] || !seen[2] {
			return errStatsSinglePass
		}
		if !fn(vals[1], vals[0], vals[2]) {
			return errStatsSinglePass
		}
		if i >= len(b) {
			return errStatsSinglePass
		}
		if b[i] == ']' {
			if i != len(b)-1 {
				return errStatsSinglePass
			}
			return nil
		}
		if b[i] != ',' {
			return errStatsSinglePass
		}
		i++
	}
}
