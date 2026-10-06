package keys

import "strings"

// LiteralPrefix marks a public key written inline instead of as a path, a
// form user.signingkey accepts: "key::ssh-ed25519 AAAA…".
const LiteralPrefix = "key::"

// Ref is a key as an account names it, in one of three forms:
//   - the path of a private key, with its public half next to it as .pub
//   - the path of a .pub file whose private half lives in an agent
//   - a public key written inline after LiteralPrefix
//
// Paths may start with "~/"; Map expands them. The functions in this
// package that take a Ref expect an expanded one.
type Ref string

// IsLiteral reports whether the key is written inline rather than named
// by a path.
func (r Ref) IsLiteral() bool { return strings.HasPrefix(string(r), LiteralPrefix) }

// IsPublic reports whether the key is a public half only: a .pub path or a
// literal, so an agent holds the private key.
func (r Ref) IsPublic() bool { return r.IsLiteral() || strings.HasSuffix(string(r), ".pub") }

// PrivatePath returns where the private half would be: the path without its
// .pub. It's "" for a literal, which has no file.
func (r Ref) PrivatePath() string {
	if r.IsLiteral() {
		return ""
	}
	return strings.TrimSuffix(string(r), ".pub")
}

// PublicPath returns the path of the public half: the path itself when it
// already ends in .pub, otherwise path + ".pub". It's "" for a literal.
func (r Ref) PublicPath() string {
	if r.IsLiteral() || r == "" {
		return ""
	}
	if strings.HasSuffix(string(r), ".pub") {
		return string(r)
	}
	return string(r) + ".pub"
}

// Public returns the key's public half as a Ref: its .pub path, or the
// literal itself. It's the form user.signingkey holds.
func (r Ref) Public() Ref {
	if r.IsLiteral() {
		return r
	}
	return Ref(r.PublicPath())
}

// Literal returns the public key a literal holds, without LiteralPrefix,
// and whether r is one.
func (r Ref) Literal() (string, bool) { return strings.CutPrefix(string(r), LiteralPrefix) }

// SameKey reports whether two refs name the same key, whichever half each
// names. No key ("") is the same as no other.
func (r Ref) SameKey(other Ref) bool { return r != "" && other != "" && r.Public() == other.Public() }

// Map rewrites the path a ref names with f, such as expanding or
// shortening "~/". A literal, or no key, stays as it is.
func (r Ref) Map(f func(path string) string) Ref {
	if r == "" || r.IsLiteral() {
		return r
	}
	return Ref(f(string(r)))
}

// String returns the ref as written, for messages and Git config.
func (r Ref) String() string { return string(r) }

// Display names the key for messages: its path, or for a literal, the key
// type and the start of the key.
func (r Ref) Display() string {
	line, ok := r.Literal()
	if !ok {
		return string(r)
	}
	fields := strings.Fields(line)
	switch {
	case len(fields) >= 2 && len(fields[1]) > 16:
		return "inline " + fields[0] + " " + fields[1][:16] + "…"
	case len(fields) >= 1:
		return "inline " + strings.Join(fields, " ")
	}
	return "an inline key"
}
