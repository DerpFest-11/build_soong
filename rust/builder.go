// Copyright 2019 The Android Open Source Project
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rust

import (
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/pathtools"

	"android/soong/android"
	"android/soong/cc"
)

var (
	_     = pctx.SourcePathVariable("rustcCmd", "${config.RustBin}/rustc")
	rustc = pctx.AndroidStaticRule("rustc",
		blueprint.RuleParams{
			Command: "$rustcCmd " +
				"-C linker=${config.RustLinker} " +
				"-C link-args=\"${crtBegin} ${config.RustLinkerArgs} ${linkFlags} ${crtEnd}\" " +
				"--emit link -o $out --emit dep-info=$out.d $in ${libFlags} $rustcFlags",
			CommandDeps: []string{"$rustcCmd"},
			// Rustc deps-info writes out make compatible dep files: https://github.com/rust-lang/rust/issues/7633
			Deps:    blueprint.DepsGCC,
			Depfile: "$out.d",
		},
		"rustcFlags", "linkFlags", "libFlags", "crtBegin", "crtEnd")

	_            = pctx.SourcePathVariable("clippyCmd", "${config.RustBin}/clippy-driver")
	clippyDriver = pctx.AndroidStaticRule("clippy",
		blueprint.RuleParams{
			Command: "$clippyCmd " +
				// Because clippy-driver uses rustc as backend, we need to have some output even during the linting.
				// Use the metadata output as it has the smallest footprint.
				"--emit metadata -o $out $in ${libFlags} " +
				"$rustcFlags $clippyFlags",
			CommandDeps: []string{"$clippyCmd"},
		},
		"rustcFlags", "libFlags", "clippyFlags")

	zip = pctx.AndroidStaticRule("zip",
		blueprint.RuleParams{
			Command:        "cat $out.rsp | tr ' ' '\\n' | tr -d \\' | sort -u > ${out}.tmp && ${SoongZipCmd} -o ${out} -C $$OUT_DIR -l ${out}.tmp",
			CommandDeps:    []string{"${SoongZipCmd}"},
			Rspfile:        "$out.rsp",
			RspfileContent: "$in",
		})
)

type buildOutput struct {
	outputFile   android.Path
	coverageFile android.Path
}

func init() {
	pctx.HostBinToolVariable("SoongZipCmd", "soong_zip")
}

func TransformSrcToBinary(ctx ModuleContext, mainSrc android.Path, deps PathDeps, flags Flags,
	outputFile android.WritablePath, linkDirs []string) buildOutput {
	flags.RustFlags = append(flags.RustFlags, "-C lto")

	return transformSrctoCrate(ctx, mainSrc, deps, flags, outputFile, "bin", linkDirs)
}

func TransformSrctoRlib(ctx ModuleContext, mainSrc android.Path, deps PathDeps, flags Flags,
	outputFile android.WritablePath, linkDirs []string) buildOutput {
	return transformSrctoCrate(ctx, mainSrc, deps, flags, outputFile, "rlib", linkDirs)
}

func TransformSrctoDylib(ctx ModuleContext, mainSrc android.Path, deps PathDeps, flags Flags,
	outputFile android.WritablePath, linkDirs []string) buildOutput {
	return transformSrctoCrate(ctx, mainSrc, deps, flags, outputFile, "dylib", linkDirs)
}

func TransformSrctoStatic(ctx ModuleContext, mainSrc android.Path, deps PathDeps, flags Flags,
	outputFile android.WritablePath, linkDirs []string) buildOutput {
	flags.RustFlags = append(flags.RustFlags, "-C lto")
	return transformSrctoCrate(ctx, mainSrc, deps, flags, outputFile, "staticlib", linkDirs)
}

func TransformSrctoShared(ctx ModuleContext, mainSrc android.Path, deps PathDeps, flags Flags,
	outputFile android.WritablePath, linkDirs []string) buildOutput {
	flags.RustFlags = append(flags.RustFlags, "-C lto")
	return transformSrctoCrate(ctx, mainSrc, deps, flags, outputFile, "cdylib", linkDirs)
}

func TransformSrctoProcMacro(ctx ModuleContext, mainSrc android.Path, deps PathDeps,
	flags Flags, outputFile android.WritablePath, linkDirs []string) buildOutput {
	return transformSrctoCrate(ctx, mainSrc, deps, flags, outputFile, "proc-macro", linkDirs)
}

func rustLibsToPaths(libs RustLibraries) android.Paths {
	var paths android.Paths
	for _, lib := range libs {
		paths = append(paths, lib.Path)
	}
	return paths
}

func transformSrctoCrate(ctx ModuleContext, main android.Path, deps PathDeps, flags Flags,
	outputFile android.WritablePath, crate_type string, linkDirs []string) buildOutput {

	var inputs android.Paths
	var implicits android.Paths
	var output buildOutput
	var libFlags, rustcFlags, linkFlags []string
	var implicitOutputs android.WritablePaths

	output.outputFile = outputFile
	crate_name := ctx.RustModule().CrateName()
	targetTriple := ctx.toolchain().RustTriple()

	inputs = append(inputs, main)

	// Collect rustc flags
	rustcFlags = append(rustcFlags, flags.GlobalRustFlags...)
	rustcFlags = append(rustcFlags, flags.RustFlags...)
	rustcFlags = append(rustcFlags, "--crate-type="+crate_type)
	if crate_name != "" {
		rustcFlags = append(rustcFlags, "--crate-name="+crate_name)
	}
	if targetTriple != "" {
		rustcFlags = append(rustcFlags, "--target="+targetTriple)
		linkFlags = append(linkFlags, "-target "+targetTriple)
	}

	// Suppress an implicit sysroot
	rustcFlags = append(rustcFlags, "--sysroot=/dev/null")

	// Collect linker flags
	linkFlags = append(linkFlags, flags.GlobalLinkFlags...)
	linkFlags = append(linkFlags, flags.LinkFlags...)

	// Collect library/crate flags
	for _, lib := range deps.RLibs {
		libFlags = append(libFlags, "--extern "+lib.CrateName+"="+lib.Path.String())
	}
	for _, lib := range deps.DyLibs {
		libFlags = append(libFlags, "--extern "+lib.CrateName+"="+lib.Path.String())
	}
	for _, proc_macro := range deps.ProcMacros {
		libFlags = append(libFlags, "--extern "+proc_macro.CrateName+"="+proc_macro.Path.String())
	}

	for _, path := range linkDirs {
		libFlags = append(libFlags, "-L "+path)
	}

	// Collect dependencies
	implicits = append(implicits, rustLibsToPaths(deps.RLibs)...)
	implicits = append(implicits, rustLibsToPaths(deps.DyLibs)...)
	implicits = append(implicits, rustLibsToPaths(deps.ProcMacros)...)
	implicits = append(implicits, deps.StaticLibs...)
	implicits = append(implicits, deps.SharedLibs...)
	if deps.CrtBegin.Valid() {
		implicits = append(implicits, deps.CrtBegin.Path(), deps.CrtEnd.Path())
	}

	if flags.Coverage {
		var gcnoFile android.WritablePath
		// Provide consistency with cc gcda output, see cc/builder.go init()
		profileEmitArg := strings.TrimPrefix(cc.PwdPrefix(), "PWD=") + "/"

		if outputFile.Ext() != "" {
			gcnoFile = android.PathForModuleOut(ctx, pathtools.ReplaceExtension(outputFile.Base(), "gcno"))
			rustcFlags = append(rustcFlags, "-Z profile-emit="+profileEmitArg+android.PathForModuleOut(
				ctx, pathtools.ReplaceExtension(outputFile.Base(), "gcda")).String())
		} else {
			gcnoFile = android.PathForModuleOut(ctx, outputFile.Base()+".gcno")
			rustcFlags = append(rustcFlags, "-Z profile-emit="+profileEmitArg+android.PathForModuleOut(
				ctx, outputFile.Base()+".gcda").String())
		}

		implicitOutputs = append(implicitOutputs, gcnoFile)
		output.coverageFile = gcnoFile
	}

	if flags.Clippy {
		clippyFile := android.PathForModuleOut(ctx, outputFile.Base()+".clippy")
		ctx.Build(pctx, android.BuildParams{
			Rule:            clippyDriver,
			Description:     "clippy " + main.Rel(),
			Output:          clippyFile,
			ImplicitOutputs: nil,
			Inputs:          inputs,
			Implicits:       implicits,
			Args: map[string]string{
				"rustcFlags":  strings.Join(rustcFlags, " "),
				"libFlags":    strings.Join(libFlags, " "),
				"clippyFlags": strings.Join(flags.ClippyFlags, " "),
			},
		})
		// Declare the clippy build as an implicit dependency of the original crate.
		implicits = append(implicits, clippyFile)
	}

	ctx.Build(pctx, android.BuildParams{
		Rule:            rustc,
		Description:     "rustc " + main.Rel(),
		Output:          outputFile,
		ImplicitOutputs: implicitOutputs,
		Inputs:          inputs,
		Implicits:       implicits,
		Args: map[string]string{
			"rustcFlags": strings.Join(rustcFlags, " "),
			"linkFlags":  strings.Join(linkFlags, " "),
			"libFlags":   strings.Join(libFlags, " "),
			"crtBegin":   deps.CrtBegin.String(),
			"crtEnd":     deps.CrtEnd.String(),
		},
	})

	return output
}

func TransformCoverageFilesToZip(ctx ModuleContext,
	covFiles android.Paths, baseName string) android.OptionalPath {
	if len(covFiles) > 0 {

		outputFile := android.PathForModuleOut(ctx, baseName+".zip")

		ctx.Build(pctx, android.BuildParams{
			Rule:        zip,
			Description: "zip " + outputFile.Base(),
			Inputs:      covFiles,
			Output:      outputFile,
		})

		return android.OptionalPathForPath(outputFile)
	}
	return android.OptionalPath{}
}
