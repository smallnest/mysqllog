package mysqllog

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LogEvent represents a slow query log event.
// "User", "Host", "Timestamp" (from SET timestamp as a time.Time), and "Statement"
// all should usually be present. Other attributes are set if found.
// Numbers are float64 or int64. Values of "Yes" or "No" are converted to bools.
type LogEvent map[string]interface{}

// Parser is a MySQL slow query log format parser.
type Parser struct {
	inHeader bool
	inQuery  bool
	lines    []string
}

// ConsumeLine consumes a line and returns a LogEvent if
// the parser recognizes a completed event.
func (p *Parser) ConsumeLine(line string) LogEvent {
	if line == "" {
		if p.inQuery {
			// We're in a new section
			event := parseEntry(p.lines)
			p.lines = append(p.lines[:0], line)
			p.inQuery = false
			p.inHeader = true
			return event
		}
		return nil
	}
	if strings.HasPrefix(line, "#") {
		// Comment line
		if p.inQuery {
			// We're in a new section
			event := parseEntry(p.lines)
			p.lines = append(p.lines[:0], line)
			p.inQuery = false
			p.inHeader = true
			return event
		}
		p.inHeader = true
		p.lines = append(p.lines, line)
		return nil
	}

	// Not a comment line
	if p.inHeader {
		p.inHeader = false
		p.inQuery = true
		p.lines = append(p.lines, line)
		return nil
	}
	if p.inQuery {
		// Keep consuming query lines
		p.lines = append(p.lines, line)
	}

	return nil
}

// Flush processes any pending lines and returns a LogEvent if one is complete.
func (p *Parser) Flush() LogEvent {
	if !p.inQuery {
		return nil
	}
	event := parseEntry(p.lines)
	p.lines = p.lines[:0]
	return event
}

var userHostAttributesRe = regexp.MustCompile(`\b(User@Host: \S+\[\w+\]+ @ (?:)(\w+)? \[\S*\])|(Id:.+)`)
var attributesRe = regexp.MustCompile(`\b([\w_]+:\s+[^\s]+)\b`)

func parseUserHostLine(line string) map[string]string {
	event := map[string]string{}
	matches := userHostAttributesRe.FindAllString(line, -1)
	for _, match := range matches {
		parts := strings.Split(match, ": ")
		switch parts[0] {
		case "User@Host":
			userHostParts := strings.Split(parts[1], "@")
			event["User"] = strings.TrimSpace(strings.Split(userHostParts[0], "[")[0])
			event["Host"] = strings.TrimSpace(strings.Split(userHostParts[1], "[")[0])
			event["IP"] = strings.TrimRight(strings.TrimSpace(strings.Split(userHostParts[1], "[")[1]), "]")
			if len(event["IP"]) == 0 {
				delete(event, "IP")
			}
			if len(event["Host"]) == 0 {
				if len(event["IP"]) > 0 {
					event["Host"] = event["IP"]
				} else {
					delete(event, "Host")
				}
			}
		}
	}
	return event
}

// parseEntry actually parses lines that belong to a log event.
func parseEntry(lines []string) LogEvent {
	event := LogEvent{}
	var i int
	var line string
	for i, line = range lines {
		if line == "" {
			continue
		}
		if line[0] != '#' {
			break
		}
		if strings.HasPrefix(line, "# User@Host") {
			fields := parseUserHostLine(line)
			for k, v := range fields {
				event[k] = v
			}
			continue
		}
		matches := attributesRe.FindAllString(line, -1)
		for _, match := range matches {
			parts := strings.Split(match, ": ")
			var attributeValue interface{}
			switch attributeTypes[parts[0]] {
			case attributeTypeString:
				attributeValue = parts[1]
			case attributeTypeBool:
				v, err := strconv.ParseBool(parts[1])
				if err == nil {
					attributeValue = v
				}
			case attributeTypeFloat:
				v, err := strconv.ParseFloat(parts[1], 64)
				if err == nil {
					attributeValue = v
				}
			case attributeTypeInt:
				v, err := strconv.ParseInt(parts[1], 10, 64)
				if err == nil {
					attributeValue = int64(v)
				}
			}

			if attributeValue == nil {
				continue
			}

			event[parts[0]] = attributeValue
		}
	}

	// See if we have lines to skip
	for ; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "use ") {
			db := strings.TrimRight(strings.Split(lines[i], " ")[1], ";\n")
			event["Database"] = db
			continue
		}
		if strings.HasPrefix(lines[i], "SET ") {
			if strings.HasPrefix(lines[i], "SET timestamp=") {
				unixTimestampString := strings.TrimRight(strings.Split(lines[i], "=")[1], ";\n")
				event["Timestamp"] = unixTimestampString
				i, err := strconv.ParseInt(unixTimestampString, 10, 64)
				if err == nil {
					event["Timestamp"] = time.Unix(i, 0).Format("2006-01-02 15:04:05")
				}
			}
			continue
		}
		break
	}

	queryLines := []string{}
	for ; i < len(lines); i++ {
		if strings.HasSuffix(lines[i], "started with:\n") {
			// Rolled over to a new log file
			break
		}
		queryLines = append(queryLines, lines[i])
	}

	event["Statement"] = strings.TrimSpace(strings.Join(queryLines, "\n"))
	return event
}
