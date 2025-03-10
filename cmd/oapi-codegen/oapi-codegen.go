// Copyright 2019 DeepMap, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"runtime/debug"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/deepmap/oapi-codegen/pkg/codegen"
	"github.com/deepmap/oapi-codegen/pkg/util"
)

func errExit(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}

var (
	flagOutputFile     string
	flagConfigFile     string
	flagOldConfigStyle bool
	flagOutputConfig   bool
	flagPrintVersion   bool
	flagPackageName    string

	// The options below are deprecated, and they will be removed in a future
	// release. Please use the new config file format.
	flagGenerate           string
	flagIncludeTags        string
	flagExcludeTags        string
	flagTemplatesDir       string
	flagImportMapping      string
	flagExcludeSchemas     string
	flagResponseTypeSuffix string
	flagAliasTypes         bool
)

type configuration struct {
	codegen.Configuration `yaml:",inline"`

	// OutputFile is the filename to output.
	OutputFile string `yaml:"output,omitempty"`
}

// This structure is deprecated. Please add no more flags here. It is here
// for backwards compatibility and it will be removed in the future.
type oldConfiguration struct {
	PackageName        string                       `yaml:"package"`
	GenerateTargets    []string                     `yaml:"generate"`
	OutputFile         string                       `yaml:"output"`
	IncludeTags        []string                     `yaml:"include-tags"`
	ExcludeTags        []string                     `yaml:"exclude-tags"`
	TemplatesDir       string                       `yaml:"templates"`
	ImportMapping      map[string]string            `yaml:"import-mapping"`
	ExcludeSchemas     []string                     `yaml:"exclude-schemas"`
	ResponseTypeSuffix string                       `yaml:"response-type-suffix"`
	Compatibility      codegen.CompatibilityOptions `yaml:"compatibility"`
}

func main() {
	flag.StringVar(&flagOutputFile, "o", "", "Where to output generated code, stdout is default")
	flag.BoolVar(&flagOldConfigStyle, "old-config-style", false, "whether to use the older style config file format")
	flag.BoolVar(&flagOutputConfig, "output-config", false, "when true, outputs a configuration file for oapi-codegen using current settings")
	flag.StringVar(&flagConfigFile, "config", "", "a YAML config file that controls oapi-codegen behavior")
	flag.BoolVar(&flagPrintVersion, "version", false, "when specified, print version and exit")
	flag.StringVar(&flagPackageName, "package", "", "The package name for generated code")

	// All flags below are deprecated, and will be removed in a future release. Please do not
	// update their behavior.
	flag.StringVar(&flagGenerate, "generate", "types,client,server,spec",
		`Comma-separated list of code to generate; valid options: "types", "client", "chi-server", "server", "gin", "gorilla", "spec", "skip-fmt", "skip-prune"`)
	flag.StringVar(&flagIncludeTags, "include-tags", "", "Only include operations with the given tags. Comma-separated list of tags.")
	flag.StringVar(&flagExcludeTags, "exclude-tags", "", "Exclude operations that are tagged with the given tags. Comma-separated list of tags.")
	flag.StringVar(&flagTemplatesDir, "templates", "", "Path to directory containing user templates")
	flag.StringVar(&flagImportMapping, "import-mapping", "", "A dict from the external reference to golang package path")
	flag.StringVar(&flagExcludeSchemas, "exclude-schemas", "", "A comma separated list of schemas which must be excluded from generation")
	flag.StringVar(&flagResponseTypeSuffix, "response-type-suffix", "", "the suffix used for responses types")
	flag.BoolVar(&flagAliasTypes, "alias-types", false, "Alias type declarations of possible")

	flag.Parse()

	if flagPrintVersion {
		bi, ok := debug.ReadBuildInfo()
		if !ok {
			fmt.Fprintln(os.Stderr, "error reading build info")
			os.Exit(1)
		}
		fmt.Println(bi.Main.Path + "/cmd/oapi-codegen")
		fmt.Println(bi.Main.Version)
		return
	}

	if flag.NArg() < 1 {
		fmt.Println("Please specify a path to a OpenAPI 3.0 spec file")
		os.Exit(1)
	}

	var opts configuration
	if !flagOldConfigStyle {
		// We simply read the configuration from disk.
		if flagConfigFile != "" {
			buf, err := ioutil.ReadFile(flagConfigFile)
			if err != nil {
				errExit("error reading config file '%s': %v", flagConfigFile, err)
			}
			err = yaml.Unmarshal(buf, &opts)
			if err != nil {
				errExit("error parsing'%s' as YAML: %v", flagConfigFile, err)
			}
		}
		var err error
		opts, err = updateConfigFromFlags(opts)
		if err != nil {
			errExit("error processing flags: %v", err)
		}
	} else {
		var oldConfig oldConfiguration
		if flagConfigFile != "" {
			buf, err := ioutil.ReadFile(flagConfigFile)
			if err != nil {
				errExit("error reading config file '%s': %v", flagConfigFile, err)
			}
			err = yaml.Unmarshal(buf, &oldConfig)
			if err != nil {
				errExit("error parsing'%s' as YAML: %v", flagConfigFile, err)
			}
		}
		opts = newConfigFromOldConfig(oldConfig)
	}

	// Ensure default values are set if user hasn't specified some needed
	// fields.
	opts.Configuration = opts.UpdateDefaults()

	// Now, ensure that the config options are valid.
	if err := opts.Validate(); err != nil {
		errExit("configuration error: %v", err)
	}

	// If the user asked to output configuration, output it to stdout and exit
	if flagOutputConfig {
		buf, err := yaml.Marshal(opts)
		if err != nil {
			errExit("error YAML marshaling configuration: %v", err)
		}
		fmt.Print(string(buf))
		return
	}

	swagger, err := util.LoadSwagger(flag.Arg(0))
	if err != nil {
		errExit("error loading swagger spec in %s\n: %s", flag.Arg(0), err)
	}

	code, err := codegen.Generate(swagger, opts.Configuration)
	if err != nil {
		errExit("error generating code: %s\n", err)
	}

	if opts.OutputFile != "" {
		err = ioutil.WriteFile(opts.OutputFile, []byte(code), 0644)
		if err != nil {
			errExit("error writing generated code to file: %s", err)
		}
	} else {
		fmt.Print(code)
	}
}

func loadTemplateOverrides(templatesDir string) (map[string]string, error) {
	var templates = make(map[string]string)

	if templatesDir == "" {
		return templates, nil
	}

	files, err := ioutil.ReadDir(templatesDir)
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		// Recursively load subdirectory files, using the path relative to the templates
		// directory as the key. This allows for overriding the files in the service-specific
		// directories (e.g. echo, chi, etc.).
		if f.IsDir() {
			subFiles, err := loadTemplateOverrides(path.Join(templatesDir, f.Name()))
			if err != nil {
				return nil, err
			}
			for subDir, subFile := range subFiles {
				templates[path.Join(f.Name(), subDir)] = subFile
			}
			continue
		}
		data, err := ioutil.ReadFile(path.Join(templatesDir, f.Name()))
		if err != nil {
			return nil, err
		}
		templates[f.Name()] = string(data)
	}

	return templates, nil
}

// updateConfigFromFlags updates a loaded configuration from flags. Flags
// override anything in the file. We generate errors for command line options
// associated with the old style configuration
func updateConfigFromFlags(cfg configuration) (configuration, error) {
	if flagPackageName != "" {
		cfg.PackageName = flagPackageName
	}

	var unsupportedFlags []string

	if flagGenerate != "types,client,server,spec" {
		unsupportedFlags = append(unsupportedFlags, "--generate")
	}
	if flagIncludeTags != "" {
		unsupportedFlags = append(unsupportedFlags, "--include-tags")
	}
	if flagExcludeTags != "" {
		unsupportedFlags = append(unsupportedFlags, "--exclude-tags")
	}
	if flagTemplatesDir != "" {
		unsupportedFlags = append(unsupportedFlags, "--templates")
	}
	if flagImportMapping != "" {
		unsupportedFlags = append(unsupportedFlags, "--import-mapping")
	}
	if flagExcludeSchemas != "" {
		unsupportedFlags = append(unsupportedFlags, "--exclude-schemas")
	}
	if flagResponseTypeSuffix != "" {
		unsupportedFlags = append(unsupportedFlags, "--response-type-suffix")
	}
	if flagAliasTypes {
		unsupportedFlags = append(unsupportedFlags, "--alias-types")
	}

	if len(unsupportedFlags) > 0 {
		return configuration{}, fmt.Errorf("flags %s aren't supported in "+
			"new config style, please use  -old-config-style or update your configuration ",
			strings.Join(unsupportedFlags, ", "))
	}

	return cfg, nil
}

// updateOldConfigFromFlags parses the flags and the config file. Anything which is
// a zerovalue in the configuration file will be replaced with the flag
// value, this means that the config file overrides flag values.
func updateOldConfigFromFlags(cfg oldConfiguration) oldConfiguration {
	if cfg.PackageName == "" {
		cfg.PackageName = flagPackageName
	}
	if cfg.GenerateTargets == nil {
		cfg.GenerateTargets = util.ParseCommandLineList(flagGenerate)
	}
	if cfg.IncludeTags == nil {
		cfg.IncludeTags = util.ParseCommandLineList(flagIncludeTags)
	}
	if cfg.ExcludeTags == nil {
		cfg.ExcludeTags = util.ParseCommandLineList(flagExcludeTags)
	}
	if cfg.TemplatesDir == "" {
		cfg.TemplatesDir = flagTemplatesDir
	}
	if cfg.ImportMapping == nil && flagImportMapping != "" {
		var err error
		cfg.ImportMapping, err = util.ParseCommandlineMap(flagImportMapping)
		if err != nil {
			errExit("error parsing import-mapping: %s\n", err)
		}
	}
	if cfg.ExcludeSchemas == nil {
		cfg.ExcludeSchemas = util.ParseCommandLineList(flagExcludeSchemas)
	}
	if cfg.OutputFile == "" {
		cfg.OutputFile = flagOutputFile
	}
	return cfg
}

func newConfigFromOldConfig(c oldConfiguration) configuration {
	// Take flags into account.
	cfg := updateOldConfigFromFlags(c)

	// Now, copy over field by field, translating flags and old values as
	// necessary.
	opts := codegen.Configuration{
		PackageName: cfg.PackageName,
	}
	opts.OutputOptions.ResponseTypeSuffix = flagResponseTypeSuffix

	for _, g := range cfg.GenerateTargets {
		switch g {
		case "client":
			opts.Generate.Client = true
		case "chi-server":
			opts.Generate.ChiServer = true
		case "server":
			opts.Generate.EchoServer = true
		case "gin":
			opts.Generate.GinServer = true
		case "gorilla":
			opts.Generate.GorillaServer = true
		case "types":
			opts.Generate.Models = true
		case "spec":
			opts.Generate.EmbeddedSpec = true
		case "skip-fmt":
			opts.OutputOptions.SkipFmt = true
		case "skip-prune":
			opts.OutputOptions.SkipPrune = true
		default:
			fmt.Printf("unknown generate option %s\n", g)
			flag.PrintDefaults()
			os.Exit(1)
		}
	}

	opts.OutputOptions.IncludeTags = cfg.IncludeTags
	opts.OutputOptions.ExcludeTags = cfg.ExcludeTags
	opts.OutputOptions.ExcludeSchemas = cfg.ExcludeSchemas

	templates, err := loadTemplateOverrides(cfg.TemplatesDir)
	if err != nil {
		errExit("error loading template overrides: %s\n", err)
	}
	opts.OutputOptions.UserTemplates = templates

	opts.ImportMapping = cfg.ImportMapping

	opts.Compatibility = cfg.Compatibility

	return configuration{
		Configuration: opts,
		OutputFile:    cfg.OutputFile,
	}
}
