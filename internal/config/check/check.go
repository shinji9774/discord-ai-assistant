// Package check は、validate タグをもとに設定値を検証し、キー名つきのエラーを作る。
package check

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/go-playground/validator/v10/non-standard/validators"
)

const (
	yamlTagName    = "yaml"
	tagOptionSep   = ","
	skippedYAMLTag = "-"
	namespaceSep   = "."
	tagNotBlank    = "notblank"

	keyedMessageFormat  = "%s: %s"
	registerErrorFormat = "register %s validation: %w"

	tagMin     = "min"
	tagMax     = "max"
	tagContain = "contains"
	tagHTTPURL = "http_url"

	minMessageFormat      = "must be %s or greater (got %v)"
	maxMessageFormat      = "must be %s or less (got %v)"
	notBlankMessage       = "must not be empty"
	containsMessageFormat = "must contain %s"
	httpURLMessageFormat  = "must be an absolute http or https URL (got %q)"
	fallbackMessageFormat = "failed %s validation"
)

var validate, setupErr = newValidate()

func newValidate() (*validator.Validate, error) {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(yamlFieldName)
	if err := v.RegisterValidation(tagNotBlank, validators.NotBlank); err != nil {
		return nil, fmt.Errorf(registerErrorFormat, tagNotBlank, err)
	}
	return v, nil
}

// yamlFieldName は、フィールドの yaml タグの名前を返す。タグが無い、または "-" のときは空文字を返す。
func yamlFieldName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get(yamlTagName), tagOptionSep)
	if name == skippedYAMLTag {
		return ""
	}
	return name
}

// Struct は v の validate タグを検証し、違反を "yaml.path: message" の形で全部まとめて返す。
func Struct(v any) error {
	if setupErr != nil {
		return setupErr
	}
	err := validate.Struct(v)
	if err == nil {
		return nil
	}
	var violations validator.ValidationErrors
	if !errors.As(err, &violations) {
		return err
	}
	problems := make([]error, 0, len(violations))
	for _, violation := range violations {
		problems = append(problems, fmt.Errorf(keyedMessageFormat, keyOf(violation), messageOf(violation)))
	}
	return errors.Join(problems...)
}

// keyOf は、Namespace の先頭の型名を落とした、yaml のキーの path を返す。
func keyOf(violation validator.FieldError) string {
	_, key, _ := strings.Cut(violation.Namespace(), namespaceSep)
	return key
}

func messageOf(violation validator.FieldError) string {
	switch violation.Tag() {
	case tagMin:
		return fmt.Sprintf(minMessageFormat, violation.Param(), violation.Value())
	case tagMax:
		return fmt.Sprintf(maxMessageFormat, violation.Param(), violation.Value())
	case tagNotBlank:
		return notBlankMessage
	case tagContain:
		return fmt.Sprintf(containsMessageFormat, violation.Param())
	case tagHTTPURL:
		return fmt.Sprintf(httpURLMessageFormat, violation.Value())
	default:
		return fmt.Sprintf(fallbackMessageFormat, violation.Tag())
	}
}
