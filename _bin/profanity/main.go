package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/blend/go-sdk/yaml"
)

const (
	// DefaultProfanityFile is the default file to use for profanity rules
	DefaultProfanityFile = "PROFANITY"
)

var rulesFile = flag.String("rules", DefaultProfanityFile, "the default rules to include for any sub-package.")
var include = flag.String("include", "", "the include file filter in glob form, can be a csv.")
var exclude = flag.String("exclude", "", "the exclude file filter in glob form, can be a csv.")
var verbose = flag.Bool("v", false, "verbose output")

func main() {
	// walk the filesystem
	// for each file named by the gob filter
	// run the rules on it
	flag.Parse()

	if rulesFile != nil && len(*rulesFile) > 0 {
		if *verbose {
			fmt.Fprintf(os.Stdout, "using rules file: %s\n", *rulesFile)
		}
	}

	if *verbose {
		if len(*include) > 0 {
			fmt.Fprintf(os.Stdout, "using include filter: %s\n", *include)
		}
		if len(*exclude) > 0 {
			fmt.Fprintf(os.Stdout, "using exclude filter: %s\n", *exclude)
		}
	}

	realizedRules := map[string][]Rule{}
	packageRules := map[string][]Rule{}

	var fileBase string
	walkErr := filepath.Walk(".", func(file string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() && strings.HasSuffix(file, ".git") {
			return filepath.SkipDir
		}
		if info.IsDir() && strings.HasSuffix(file, "_bin") {
			return filepath.SkipDir
		}
		if info.IsDir() {
			return nil
		}

		fileBase = filepath.Base(file)
		if *verbose {
			fmt.Fprintf(os.Stdout, "%s", ColorLightWhite.Apply(file))
		}

		if len(*include) > 0 {
			if matches, err := globAnyMatch(*include, file); err != nil {
				return err
			} else if !matches {
				if *verbose {
					fmt.Fprintf(os.Stdout, ".. skipping\n")
				}
				return nil
			}
		}

		if len(*exclude) > 0 {
			if matches, err := globAnyMatch(*exclude, file); err != nil {
				return err
			} else if matches {
				if *verbose {
					fmt.Fprintf(os.Stdout, ".. skipping\n")
				}
				return nil
			}
		}

		if matches, err := filepath.Match(DefaultProfanityFile, fileBase); err != nil {
			return err
		} else if matches {
			if *verbose {
				fmt.Fprintf(os.Stdout, ".. skipping\n")
			}
			return nil
		}

		rules, err := getRules(realizedRules, packageRules, filepath.Dir(file))
		if err != nil {
			return err
		}

		contents, err := ioutil.ReadFile(file)
		if err != nil {
			return err
		}

		for _, rule := range rules {
			if matches, err := rule.ShouldInclude(file); err != nil {
				return err
			} else if !matches {
				continue
			}

			if matches, err := rule.ShouldExclude(file); err != nil {
				return err
			} else if matches {
				continue
			}

			if err := rule.Apply(contents); err != nil {
				fileMessage := ColorLightWhite.Apply(file)
				failedMessage := ColorRed.Apply("failed")
				errMessage := fmt.Sprintf("%+v", err)

				return fmt.Errorf("\n\t%s %s: %s\n\t%s: %s\n\t%s: %s\n\t%s: %s\n\t%s: %s",
					fileMessage,
					failedMessage,
					errMessage,
					ColorLightWhite.Apply("message"),
					rule.Message,
					ColorLightWhite.Apply("rules file"),
					rule.File,
					ColorLightWhite.Apply("include"),
					rule.Include,
					ColorLightWhite.Apply("exclude"),
					rule.Exclude)

			}
		}

		if *verbose {
			fmt.Fprintf(os.Stdout, " ... %s\n", ColorGreen.Apply("ok!"))
		}

		return nil
	})

	if walkErr != nil {
		fmt.Fprintf(os.Stderr, "%+v\n\n", walkErr)
		os.Exit(1)
		return
	}
	os.Exit(0)
}

// globAnyMatch tests if a file matches a (potentially) csv of glob filters.
func globAnyMatch(filter, file string) (bool, error) {
	parts := strings.Split(filter, ",")
	for _, part := range parts {
		if matches, err := filepath.Match(strings.TrimSpace(part), file); err != nil {
			return false, err
		} else if matches {
			return true, nil
		}

		if matches, err := filepath.Match(strings.TrimSpace(part), filepath.Base(file)); err != nil {
			return false, err
		} else if matches {
			return true, nil
		}
	}
	return false, nil
}

func getRules(realizedRules map[string][]Rule, packageRules map[string][]Rule, path string) ([]Rule, error) {
	if rules, hasRules := realizedRules[path]; hasRules {
		return rules, nil
	}

	rules, err := discoverRules(packageRules, path)
	if err != nil {
		return nil, err
	}
	realizedRules[path] = rules
	return rules, nil
}

func discoverRules(packageRules map[string][]Rule, path string) ([]Rule, error) {
	rules, err := localRules(packageRules, path)
	if err != nil {
		return nil, err
	}

	for key, inheritedRules := range packageRules {
		if strings.HasPrefix(path, key) && key != path {
			rules = append(inheritedRules, rules...)
		}
	}

	// always include rules from "." if they were set
	if rootRules, hasRootRules := packageRules["."]; hasRootRules && path != "." {
		rules = append(rootRules, rules...)
	}

	return rules, nil
}

func localRules(packageRules map[string][]Rule, path string) ([]Rule, error) {
	profanityPath := filepath.Join(path, *rulesFile)
	if _, err := os.Stat(profanityPath); err != nil {
		return nil, nil
	}

	rules, err := deserializeRules(profanityPath)
	if err != nil {
		return nil, err
	}
	packageRules[path] = rules
	return rules, nil
}

func deserializeRules(path string) (rules []Rule, err error) {
	var contents []byte
	contents, err = ioutil.ReadFile(path)
	if err != nil {
		return
	}
	var fileRules []Rule
	err = yaml.Unmarshal(contents, &fileRules)
	if err != nil {
		return
	}
	rules = make([]Rule, len(fileRules))
	for index, fileRule := range fileRules {
		rule := fileRule
		rule.File = path
		rules[index] = rule
	}
	return
}

// Contains creates a simple contains rule.
func Contains(value string) RuleFunc {
	return func(contents []byte) error {
		if strings.Contains(string(contents), value) {
			return fmt.Errorf("contains: \"%s\"", value)
		}
		return nil
	}
}

// NotContains creates a simple contains rule.
func NotContains(value string) RuleFunc {
	return func(contents []byte) error {
		if !strings.Contains(string(contents), value) {
			return fmt.Errorf("not contains: \"%s\"", value)
		}
		return nil
	}
}

// Regex creates a new regex filter rule.
func Regex(expr string) RuleFunc {
	regex := regexp.MustCompile(expr)
	return func(contents []byte) error {
		if regex.Match(contents) {
			return fmt.Errorf("regexp match: \"%s\"", expr)
		}
		return nil
	}
}

// Rule is a serialized rule.
type Rule struct {
	// File is the rules file path the rule came from.
	File string `yaml:"-"`
	// Message is a descriptive message for the rule.
	Message string `yaml:"message,omitempty"`
	// Contains implies we should fail if a file contains a given string.
	Contains string `yaml:"contains,omitempty"`
	// Contains implies we should fail if a file doesn't contains a given string.
	NotContains string `yaml:"notContains,omitempty"`
	// Regex implies we should fail if a file matches a given regex.
	Regex string `yaml:"regex,omitempty"`
	// Include sets a glob filter for file inclusion by filename.
	Include string `yaml:"include,omitempty"`
	// Exclude sets a glob filter for file exclusion by filename.
	Exclude string `yaml:"exclude,omitempty"`
}

// ShouldInclude returns if we should include a file for a given rule.
// If the `.Include` field is unset, this will alway return true.
func (r Rule) ShouldInclude(file string) (bool, error) {
	if len(r.Include) == 0 {
		return true, nil
	}
	return globAnyMatch(r.Include, file)
}

// ShouldExclude returns if we should include a file for a given rule.
// If the `.Include` field is unset, this will alway return true.
func (r Rule) ShouldExclude(file string) (bool, error) {
	if len(r.Exclude) == 0 {
		return false, nil
	}
	return globAnyMatch(r.Exclude, file)
}

// Apply applies the rule.
func (r Rule) Apply(contents []byte) error {
	if len(r.Contains) > 0 {
		return Contains(r.Contains)(contents)
	}
	if len(r.NotContains) > 0 {
		return NotContains(r.NotContains)(contents)
	}
	if len(r.Regex) > 0 {
		return Regex(r.Regex)(contents)
	}
	return fmt.Errorf("no rule set")
}

// RuleFunc is a function that evaluates a corpus.
type RuleFunc func([]byte) error

// AnsiColor represents an ansi color code fragment.
type AnsiColor string

// escaped escapes the color for use in the terminal.
func (acc AnsiColor) escaped() string {
	return "\033[" + string(acc)
}

// Apply returns a string with the color code applied.
func (acc AnsiColor) Apply(text string) string {
	return acc.escaped() + text + ColorReset.escaped()
}

const (
	// ColorBlack is the posix escape code fragment for black.
	ColorBlack AnsiColor = "30m"

	// ColorRed is the posix escape code fragment for red.
	ColorRed AnsiColor = "31m"

	// ColorGreen is the posix escape code fragment for green.
	ColorGreen AnsiColor = "32m"

	// ColorYellow is the posix escape code fragment for yellow.
	ColorYellow AnsiColor = "33m"

	// ColorBlue is the posix escape code fragment for blue.
	ColorBlue AnsiColor = "34m"

	// ColorPurple is the posix escape code fragement for magenta (purple)
	ColorPurple AnsiColor = "35m"

	// ColorCyan is the posix escape code fragement for cyan.
	ColorCyan AnsiColor = "36m"

	// ColorWhite is the posix escape code fragment for white.
	ColorWhite AnsiColor = "37m"

	// ColorLightBlack is the posix escape code fragment for black.
	ColorLightBlack AnsiColor = "90m"

	// ColorLightRed is the posix escape code fragment for red.
	ColorLightRed AnsiColor = "91m"

	// ColorLightGreen is the posix escape code fragment for green.
	ColorLightGreen AnsiColor = "92m"

	// ColorLightYellow is the posix escape code fragment for yellow.
	ColorLightYellow AnsiColor = "93m"

	// ColorLightBlue is the posix escape code fragment for blue.
	ColorLightBlue AnsiColor = "94m"

	// ColorLightPurple is the posix escape code fragement for magenta (purple)
	ColorLightPurple AnsiColor = "95m"

	// ColorLightCyan is the posix escape code fragement for cyan.
	ColorLightCyan AnsiColor = "96m"

	// ColorLightWhite is the posix escape code fragment for white.
	ColorLightWhite AnsiColor = "97m"

	// ColorGray is an alias to ColorLightWhite to preserve backwards compatibility.
	ColorGray AnsiColor = ColorLightBlack

	// ColorReset is the posix escape code fragment to reset all formatting.
	ColorReset AnsiColor = "0m"
)
