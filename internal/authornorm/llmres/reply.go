package llmres

import (
	"bytes"
	"encoding/json"
	"io"
)

// The reply contract of design doc section 2: the model never writes text —
// it assigns every source token a role from a closed list, reports the name
// form and the written order, and may ask for the two closed case fixes.
// There is no confidence field: cannot_tell is a refusal, not a probability.

// Form is what the token sequence names.
type Form string

// The closed set of forms.
const (
	FormPerson          Form = "person"
	FormCollective      Form = "collective"
	FormMultiplePersons Form = "multiple_persons"
	FormPlaceholder     Form = "placeholder"
	FormNotAName        Form = "not_a_name"
	FormCannotTell      Form = "cannot_tell"
)

// Forms returns the closed set of forms in canonical order.
func Forms() []Form {
	return []Form{FormPerson, FormCollective, FormMultiplePersons, FormPlaceholder, FormNotAName, FormCannotTell}
}

func (f Form) valid() bool {
	for _, known := range Forms() {
		if f == known {
			return true
		}
	}
	return false
}

// Order is the written order of the name tokens in the source.
type Order string

// The closed set of orders.
const (
	OrderGivenFirst    Order = "given_first"
	OrderFamilyFirst   Order = "family_first"
	OrderSingleName    Order = "single_name"
	OrderNotApplicable Order = "not_applicable"
)

// Orders returns the closed set of orders in canonical order.
func Orders() []Order {
	return []Order{OrderGivenFirst, OrderFamilyFirst, OrderSingleName, OrderNotApplicable}
}

func (o Order) valid() bool {
	for _, known := range Orders() {
		if o == known {
			return true
		}
	}
	return false
}

// Role is the role the model assigns to one source token.
type Role string

// The closed set of roles.
const (
	RoleGiven      Role = "given"
	RoleAdditional Role = "additional"
	RoleFamily     Role = "family"
	RoleParticle   Role = "particle"
	RolePrefix     Role = "prefix"
	RoleSuffix     Role = "suffix"
	RoleNickname   Role = "nickname"
	RoleSingleName Role = "single_name"
	RoleSeparator  Role = "separator"
	RoleFiller     Role = "filler"
)

// Roles returns the closed set of roles in canonical order.
func Roles() []Role {
	return []Role{RoleGiven, RoleAdditional, RoleFamily, RoleParticle, RolePrefix,
		RoleSuffix, RoleNickname, RoleSingleName, RoleSeparator, RoleFiller}
}

func (r Role) valid() bool {
	for _, known := range Roles() {
		if r == known {
			return true
		}
	}
	return false
}

// nameRole reports a role that carries a part of the person's name, as
// opposed to particles, affixes and separators around it.
func (r Role) nameRole() bool {
	return r == RoleGiven || r == RoleAdditional || r == RoleFamily || r == RoleSingleName
}

// CaseFixKind is one of the two admitted case transformations.
type CaseFixKind string

// The closed set of case fixes.
const (
	// CaseFixCapitalize upper-cases the first letter and lower-cases the rest.
	CaseFixCapitalize CaseFixKind = "capitalize"
	// CaseFixCapitalizeParts capitalizes every hyphen- or apostrophe-separated
	// part of the token.
	CaseFixCapitalizeParts CaseFixKind = "capitalize_parts"
)

// CaseFixKinds returns the closed set of case fixes in canonical order.
func CaseFixKinds() []CaseFixKind {
	return []CaseFixKind{CaseFixCapitalize, CaseFixCapitalizeParts}
}

func (k CaseFixKind) valid() bool {
	return k == CaseFixCapitalize || k == CaseFixCapitalizeParts
}

// RoleAssignment binds one token index to its role.
type RoleAssignment struct {
	I    int  `json:"i"`
	Role Role `json:"role"`
}

// CaseFix asks for a case transformation of one token.
type CaseFix struct {
	I  int         `json:"i"`
	To CaseFixKind `json:"to"`
}

// Reply is the parsed model answer. It carries no free text by construction.
type Reply struct {
	Form    Form             `json:"form"`
	Order   Order            `json:"order"`
	Roles   []RoleAssignment `json:"roles"`
	CaseFix []CaseFix        `json:"case_fix"`
}

// Refused reports a cannot_tell answer: a refusal, not a result.
func (r *Reply) Refused() bool { return r.Form == FormCannotTell }

// wireReply mirrors the reply JSON with pointers so a missing field is
// distinguishable from an empty one; the contract requires every field.
type wireReply struct {
	Form    *string        `json:"form"`
	Order   *string        `json:"order"`
	Roles   *[]wireRole    `json:"roles"`
	CaseFix *[]wireCaseFix `json:"case_fix"`
}

type wireRole struct {
	I    *int    `json:"i"`
	Role *string `json:"role"`
}

type wireCaseFix struct {
	I  *int    `json:"i"`
	To *string `json:"to"`
}

// ParseReply is validator V1: the strict parsing of the model's reply. The
// SDK's own parsing is not trusted — the bytes are re-read here against the
// closed schema: all four fields required, unknown fields rejected, enums
// from the closed sets, no text before or after the JSON value.
func ParseReply(raw []byte) (Reply, error) {
	w, err := decodeWireReply(raw)
	if err != nil {
		return Reply{}, err
	}
	if w.Form == nil || w.Order == nil || w.Roles == nil || w.CaseFix == nil {
		return Reply{}, v1Violation("form, order, roles and case_fix are all required")
	}
	rep := Reply{Form: Form(*w.Form), Order: Order(*w.Order)}
	if !rep.Form.valid() {
		return Reply{}, v1Violation("form is outside the closed set")
	}
	if !rep.Order.valid() {
		return Reply{}, v1Violation("order is outside the closed set")
	}
	roles, err := parseWireRoles(*w.Roles)
	if err != nil {
		return Reply{}, err
	}
	rep.Roles = roles
	fixes, err := parseWireCaseFixes(*w.CaseFix)
	if err != nil {
		return Reply{}, err
	}
	rep.CaseFix = fixes
	if rep.Roles == nil {
		rep.Roles = []RoleAssignment{}
	}
	return rep, nil
}

// v1Violation builds a V1 failure: the reply broke the parsing contract.
func v1Violation(detail string) error {
	return &Violation{Validator: validatorV1, Detail: detail}
}

// decodeWireReply decodes the wire struct strictly: unknown fields are
// rejected, and any second value after the JSON object — prose, a Markdown
// fence tail — makes the reply invalid.
func decodeWireReply(raw []byte) (wireReply, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var w wireReply
	if err := dec.Decode(&w); err != nil {
		return wireReply{}, v1Violation("reply is not the schema's JSON object")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return wireReply{}, v1Violation("unexpected content after the JSON value")
	}
	return w, nil
}

// parseWireRoles converts the wire role entries, requiring both fields, a
// non-negative index and a role from the closed set.
func parseWireRoles(wrs []wireRole) ([]RoleAssignment, error) {
	var roles []RoleAssignment
	for _, wr := range wrs {
		if wr.I == nil || wr.Role == nil {
			return nil, v1Violation("every role entry needs both i and role")
		}
		if *wr.I < 0 {
			return nil, v1Violation("role index must be non-negative")
		}
		role := Role(*wr.Role)
		if !role.valid() {
			return nil, v1Violation("role is outside the closed set")
		}
		roles = append(roles, RoleAssignment{I: *wr.I, Role: role})
	}
	return roles, nil
}

// parseWireCaseFixes converts the wire case_fix entries under the same
// contract as the roles.
func parseWireCaseFixes(wcs []wireCaseFix) ([]CaseFix, error) {
	var fixes []CaseFix
	for _, wc := range wcs {
		if wc.I == nil || wc.To == nil {
			return nil, v1Violation("every case_fix entry needs both i and to")
		}
		if *wc.I < 0 {
			return nil, v1Violation("case_fix index must be non-negative")
		}
		to := CaseFixKind(*wc.To)
		if !to.valid() {
			return nil, v1Violation("case_fix target is outside the closed set")
		}
		fixes = append(fixes, CaseFix{I: *wc.I, To: to})
	}
	return fixes, nil
}

// ReplySchema is the strict JSON schema sent to the model. The enum lists
// mirror the Go closed sets exactly; TestReplySchemaMatchesClosedSets guards
// against drift between the two.
const ReplySchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["form", "order", "roles", "case_fix"],
  "properties": {
    "form": {"$ref": "#/$defs/form"},
    "order": {"$ref": "#/$defs/order"},
    "roles": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["i", "role"],
        "properties": {
          "i": {"type": "integer", "minimum": 0},
          "role": {"$ref": "#/$defs/role"}
        }
      }
    },
    "case_fix": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["i", "to"],
        "properties": {
          "i": {"type": "integer", "minimum": 0},
          "to": {"$ref": "#/$defs/caseFix"}
        }
      }
    }
  },
  "$defs": {
    "form": {"enum": ["person", "collective", "multiple_persons", "placeholder", "not_a_name", "cannot_tell"]},
    "order": {"enum": ["given_first", "family_first", "single_name", "not_applicable"]},
    "role": {"enum": ["given", "additional", "family", "particle", "prefix", "suffix", "nickname", "single_name", "separator", "filler"]},
    "caseFix": {"enum": ["capitalize", "capitalize_parts"]}
  }
}`
