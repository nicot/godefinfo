package main

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func parsePackage(filename string, src []byte) (files []*ast.File, err error) {
	if src == nil {
		src, err = ioutil.ReadFile(filename)
		if err != nil {
			return nil, err
		}
	}

	// Treat an unrecoverable parse error on the primary file
	// as fatal, but otherwise be tolerant of errors.
	f, err := parser.ParseFile(fset, filename, src, 0)
	if f == nil || (*strict && err != nil) {
		return nil, err
	}
	files = append(files, f)

	fileFilter := func(fi os.FileInfo) bool {
		if !strings.HasSuffix(fi.Name(), ".go") {
			return false
		}

		// We already parsed the primary file, so don't reparse it.
		if fi.Name() == filepath.Base(filename) {
			return false
		}

		// Include *_test.go files only if the primary file is a test file.
		includeTestFiles := strings.HasSuffix(filename, "_test.go")
		return includeTestFiles || !strings.HasSuffix(fi.Name(), "_test.go")
	}

	pkgs, err := parser.ParseDir(fset, filepath.Dir(filename), fileFilter, 0)
	if err != nil {
		if *strict {
			return nil, err
		}
		dlog.Println(err)
	}
	for pkgName, pkg := range pkgs {
		if pkgName == f.Name.Name {
			for _, f := range pkg.Files {
				files = append(files, f)
			}
		}
	}
	return files, nil
}

// deepRecvType gets the embedded struct's name that the method or
// field is actually defined on, not just the original/outer recv
// type.
func deepRecvType(sel *types.Selection) types.Type {
	var offset int
	offset = 1
	if sel.Kind() == types.MethodVal || sel.Kind() == types.MethodExpr {
		offset = 0
	}

	typ := sel.Recv()
	idx := sel.Index()
	for k, i := range idx[:len(idx)-offset] {
		final := k == len(idx)-offset-1
		t := getMethod(typ, i, final, sel.Kind() != types.FieldVal)
		if t == nil {
			dlog.Printf("failed to get method/field at index %v on recv %s", idx, typ)
			return nil
		}
		typ = t.Type()
	}
	return typ
}

func dereferenceType(typ types.Type) types.Type {
	if typ, ok := typ.(*types.Pointer); ok {
		return typ.Elem()
	}
	return typ
}

func typeName(typ types.Type) (pkg, name string, ok bool) {
	//TODO Don't just bail on composite types.
	switch typ := typ.(type) {
	case *types.Named:
		return typ.Obj().Pkg().Path(), typ.Obj().Name(), true
	case *types.Basic:
		return "builtin", typ.Name(), true
	}
	return "", "", false
}

func getMethod(typ types.Type, idx int, final bool, method bool) (obj types.Object) {
	switch obj := typ.(type) {
	case *types.Pointer:
		return getMethod(obj.Elem(), idx, final, method)

	case *types.Named:
		if final && method {
			switch obj2 := dereferenceType(obj.Underlying()).(type) {
			case *types.Interface:
				recvObj := obj2.Method(idx).Type().(*types.Signature).Recv()
				if recvObj.Type() == obj.Underlying() {
					return obj.Obj()
				}
				return recvObj
			}
			return obj.Method(idx).Type().(*types.Signature).Recv()
		}
		return getMethod(obj.Underlying(), idx, final, method)

	case *types.Struct:
		return obj.Field(idx)

	case *types.Interface:
		// Our index is among all methods, but we want to get the
		// interface that defines the method at our index.
		return obj.Method(idx).Type().(*types.Signature).Recv()
	}
	return nil
}

func objectString(obj types.Object) string {
	if obj.Pkg() != nil {
		return fmt.Sprintf("%s %s", obj.Pkg().Path(), obj.Name())
	}
	return obj.Name()
}

var systemImp = importer.Default()

func makeImporter() types.Importer {
	imp := systemImp
	if !*importsrc {
		return imp
	}

	if imp, ok := imp.(types.ImporterFrom); ok {
		return &sourceImporterFrom{
			ImporterFrom: imp,
			cached:       map[importerPkgKey]*types.Package{},
		}
	}
	return imp
}

type importerPkgKey struct{ path, srcDir string }

type sourceImporterFrom struct {
	types.ImporterFrom

	cached map[importerPkgKey]*types.Package
}

func (s *sourceImporterFrom) Import(path string) (*types.Package, error) {
	panic("I don't think so.")
	return s.ImportFrom(path, "" /* no vendoring */, 0)
}

var _ (types.ImporterFrom) = (*sourceImporterFrom)(nil)

func (s *sourceImporterFrom) ImportFrom(path, srcDir string, mode types.ImportMode) (*types.Package, error) {
	pkg, err := s.ImporterFrom.ImportFrom(path, srcDir, mode)
	if pkg != nil {
		return pkg, err
	}

	key := importerPkgKey{path, srcDir}
	if pkg := s.cached[key]; pkg != nil && pkg.Complete() {
		return pkg, nil
	}

	if *debug {
		t0 := time.Now()
		defer func() {
			dlog.Printf("source import of %s took %s", path, time.Since(t0))
		}()
	}

	// Otherwise, parse from source.
	pkgs, err := parser.ParseDir(fset, srcDir, func(fi os.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), ".go") && !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, err
	}
	var astPkg *ast.Package
	for pkgName, pkg := range pkgs {
		if pkgName != "main" && !strings.HasSuffix(pkgName, "_test") {
			astPkg = pkg
			break
		}
	}
	if astPkg == nil {
		return nil, fmt.Errorf("ImportFrom: no suitable package found (import path %q, dir %q)", path, srcDir)
	}

	pkgFiles := make([]*ast.File, 0, len(astPkg.Files))
	for _, f := range astPkg.Files {
		pkgFiles = append(pkgFiles, f)
	}

	conf := types.Config{
		Importer:                 systemImp,
		FakeImportC:              true,
		IgnoreFuncBodies:         true,
		DisableUnusedImportCheck: true,
		Error: func(error) {},
	}
	pkg, err = conf.Check(path, fset, pkgFiles, nil)
	if pkg != nil {
		s.cached[key] = pkg
	}
	return pkg, err
}

////////////////////////////////////////////////////////////////////////////////////////
// The below code is copied from
// https://raw.githubusercontent.com/golang/tools/c86fe5956d4575f29850535871a97abbd403a145/go/ast/astutil/enclosing.go
// to eliminate external (non-stdlib) dependencies.
////////////////////////////////////////////////////////////////////////////////////////

func pathEnclosingInterval(root *ast.File, start, end token.Pos) (path []ast.Node, exact bool) {
	// fmt.Printf("EnclosingInterval %d %d\n", start, end) // debugging

	// Precondition: node.[Pos..End) and adjoining whitespace contain [start, end).
	var visit func(node ast.Node) bool
	visit = func(node ast.Node) bool {
		path = append(path, node)

		nodePos := node.Pos()
		nodeEnd := node.End()

		// fmt.Printf("visit(%T, %d, %d)\n", node, nodePos, nodeEnd) // debugging

		// Intersect [start, end) with interval of node.
		if start < nodePos {
			start = nodePos
		}
		if end > nodeEnd {
			end = nodeEnd
		}

		// Find sole child that contains [start, end).
		children := childrenOf(node)
		l := len(children)
		for i, child := range children {
			// [childPos, childEnd) is unaugmented interval of child.
			childPos := child.Pos()
			childEnd := child.End()

			// [augPos, augEnd) is whitespace-augmented interval of child.
			augPos := childPos
			augEnd := childEnd
			if i > 0 {
				augPos = children[i-1].End() // start of preceding whitespace
			}
			if i < l-1 {
				nextChildPos := children[i+1].Pos()
				// Does [start, end) lie between child and next child?
				if start >= augEnd && end <= nextChildPos {
					return false // inexact match
				}
				augEnd = nextChildPos // end of following whitespace
			}

			// fmt.Printf("\tchild %d: [%d..%d)\tcontains interval [%d..%d)?\n",
			// 	i, augPos, augEnd, start, end) // debugging

			// Does augmented child strictly contain [start, end)?
			if augPos <= start && end <= augEnd {
				_, isToken := child.(tokenNode)
				return isToken || visit(child)
			}

			// Does [start, end) overlap multiple children?
			// i.e. left-augmented child contains start
			// but LR-augmented child does not contain end.
			if start < childEnd && end > augEnd {
				break
			}
		}

		// No single child contained [start, end),
		// so node is the result.  Is it exact?

		// (It's tempting to put this condition before the
		// child loop, but it gives the wrong result in the
		// case where a node (e.g. ExprStmt) and its sole
		// child have equal intervals.)
		if start == nodePos && end == nodeEnd {
			return true // exact match
		}

		return false // inexact: overlaps multiple children
	}

	if start > end {
		start, end = end, start
	}

	if start < root.End() && end > root.Pos() {
		if start == end {
			end = start + 1 // empty interval => interval of size 1
		}
		exact = visit(root)

		// Reverse the path:
		for i, l := 0, len(path); i < l/2; i++ {
			path[i], path[l-1-i] = path[l-1-i], path[i]
		}
	} else {
		// Selection lies within whitespace preceding the
		// first (or following the last) declaration in the file.
		// The result nonetheless always includes the ast.File.
		path = append(path, root)
	}

	return
}

// tokenNode is a dummy implementation of ast.Node for a single token.
// They are used transiently by PathEnclosingInterval but never escape
// this package.
//
type tokenNode struct {
	pos token.Pos
	end token.Pos
}

func (n tokenNode) Pos() token.Pos {
	return n.pos
}

func (n tokenNode) End() token.Pos {
	return n.end
}

func tok(pos token.Pos, len int) ast.Node {
	return tokenNode{pos, pos + token.Pos(len)}
}

// childrenOf returns the direct non-nil children of ast.Node n.
// It may include fake ast.Node implementations for bare tokens.
// it is not safe to call (e.g.) ast.Walk on such nodes.
//
func childrenOf(n ast.Node) []ast.Node {
	var children []ast.Node

	// First add nodes for all true subtrees.
	ast.Inspect(n, func(node ast.Node) bool {
		if node == n { // push n
			return true // recur
		}
		if node != nil { // push child
			children = append(children, node)
		}
		return false // no recursion
	})

	// Then add fake Nodes for bare tokens.
	switch n := n.(type) {
	case *ast.ArrayType:
		children = append(children,
			tok(n.Lbrack, len("[")),
			tok(n.Elt.End(), len("]")))

	case *ast.AssignStmt:
		children = append(children,
			tok(n.TokPos, len(n.Tok.String())))

	case *ast.BasicLit:
		children = append(children,
			tok(n.ValuePos, len(n.Value)))

	case *ast.BinaryExpr:
		children = append(children, tok(n.OpPos, len(n.Op.String())))

	case *ast.BlockStmt:
		children = append(children,
			tok(n.Lbrace, len("{")),
			tok(n.Rbrace, len("}")))

	case *ast.BranchStmt:
		children = append(children,
			tok(n.TokPos, len(n.Tok.String())))

	case *ast.CallExpr:
		children = append(children,
			tok(n.Lparen, len("(")),
			tok(n.Rparen, len(")")))
		if n.Ellipsis != 0 {
			children = append(children, tok(n.Ellipsis, len("...")))
		}

	case *ast.CaseClause:
		if n.List == nil {
			children = append(children,
				tok(n.Case, len("default")))
		} else {
			children = append(children,
				tok(n.Case, len("case")))
		}
		children = append(children, tok(n.Colon, len(":")))

	case *ast.ChanType:
		switch n.Dir {
		case ast.RECV:
			children = append(children, tok(n.Begin, len("<-chan")))
		case ast.SEND:
			children = append(children, tok(n.Begin, len("chan<-")))
		case ast.RECV | ast.SEND:
			children = append(children, tok(n.Begin, len("chan")))
		}

	case *ast.CommClause:
		if n.Comm == nil {
			children = append(children,
				tok(n.Case, len("default")))
		} else {
			children = append(children,
				tok(n.Case, len("case")))
		}
		children = append(children, tok(n.Colon, len(":")))

	case *ast.Comment:
		// nop

	case *ast.CommentGroup:
		// nop

	case *ast.CompositeLit:
		children = append(children,
			tok(n.Lbrace, len("{")),
			tok(n.Rbrace, len("{")))

	case *ast.DeclStmt:
		// nop

	case *ast.DeferStmt:
		children = append(children,
			tok(n.Defer, len("defer")))

	case *ast.Ellipsis:
		children = append(children,
			tok(n.Ellipsis, len("...")))

	case *ast.EmptyStmt:
		// nop

	case *ast.ExprStmt:
		// nop

	case *ast.Field:
		// TODO(adonovan): Field.{Doc,Comment,Tag}?

	case *ast.FieldList:
		children = append(children,
			tok(n.Opening, len("(")),
			tok(n.Closing, len(")")))

	case *ast.File:
		// TODO test: Doc
		children = append(children,
			tok(n.Package, len("package")))

	case *ast.ForStmt:
		children = append(children,
			tok(n.For, len("for")))

	case *ast.FuncDecl:
		// TODO(adonovan): FuncDecl.Comment?

		// Uniquely, FuncDecl breaks the invariant that
		// preorder traversal yields tokens in lexical order:
		// in fact, FuncDecl.Recv precedes FuncDecl.Type.Func.
		//
		// As a workaround, we inline the case for FuncType
		// here and order things correctly.
		//
		children = nil // discard ast.Walk(FuncDecl) info subtrees
		children = append(children, tok(n.Type.Func, len("func")))
		if n.Recv != nil {
			children = append(children, n.Recv)
		}
		children = append(children, n.Name)
		if n.Type.Params != nil {
			children = append(children, n.Type.Params)
		}
		if n.Type.Results != nil {
			children = append(children, n.Type.Results)
		}
		if n.Body != nil {
			children = append(children, n.Body)
		}

	case *ast.FuncLit:
		// nop

	case *ast.FuncType:
		if n.Func != 0 {
			children = append(children,
				tok(n.Func, len("func")))
		}

	case *ast.GenDecl:
		children = append(children,
			tok(n.TokPos, len(n.Tok.String())))
		if n.Lparen != 0 {
			children = append(children,
				tok(n.Lparen, len("(")),
				tok(n.Rparen, len(")")))
		}

	case *ast.GoStmt:
		children = append(children,
			tok(n.Go, len("go")))

	case *ast.Ident:
		children = append(children,
			tok(n.NamePos, len(n.Name)))

	case *ast.IfStmt:
		children = append(children,
			tok(n.If, len("if")))

	case *ast.ImportSpec:
		// TODO(adonovan): ImportSpec.{Doc,EndPos}?

	case *ast.IncDecStmt:
		children = append(children,
			tok(n.TokPos, len(n.Tok.String())))

	case *ast.IndexExpr:
		children = append(children,
			tok(n.Lbrack, len("{")),
			tok(n.Rbrack, len("}")))

	case *ast.InterfaceType:
		children = append(children,
			tok(n.Interface, len("interface")))

	case *ast.KeyValueExpr:
		children = append(children,
			tok(n.Colon, len(":")))

	case *ast.LabeledStmt:
		children = append(children,
			tok(n.Colon, len(":")))

	case *ast.MapType:
		children = append(children,
			tok(n.Map, len("map")))

	case *ast.ParenExpr:
		children = append(children,
			tok(n.Lparen, len("(")),
			tok(n.Rparen, len(")")))

	case *ast.RangeStmt:
		children = append(children,
			tok(n.For, len("for")),
			tok(n.TokPos, len(n.Tok.String())))

	case *ast.ReturnStmt:
		children = append(children,
			tok(n.Return, len("return")))

	case *ast.SelectStmt:
		children = append(children,
			tok(n.Select, len("select")))

	case *ast.SelectorExpr:
		// nop

	case *ast.SendStmt:
		children = append(children,
			tok(n.Arrow, len("<-")))

	case *ast.SliceExpr:
		children = append(children,
			tok(n.Lbrack, len("[")),
			tok(n.Rbrack, len("]")))

	case *ast.StarExpr:
		children = append(children, tok(n.Star, len("*")))

	case *ast.StructType:
		children = append(children, tok(n.Struct, len("struct")))

	case *ast.SwitchStmt:
		children = append(children, tok(n.Switch, len("switch")))

	case *ast.TypeAssertExpr:
		children = append(children,
			tok(n.Lparen-1, len(".")),
			tok(n.Lparen, len("(")),
			tok(n.Rparen, len(")")))

	case *ast.TypeSpec:
		// TODO(adonovan): TypeSpec.{Doc,Comment}?

	case *ast.TypeSwitchStmt:
		children = append(children, tok(n.Switch, len("switch")))

	case *ast.UnaryExpr:
		children = append(children, tok(n.OpPos, len(n.Op.String())))

	case *ast.ValueSpec:
		// TODO(adonovan): ValueSpec.{Doc,Comment}?

	case *ast.BadDecl, *ast.BadExpr, *ast.BadStmt:
		// nop
	}

	// TODO(adonovan): opt: merge the logic of ast.Inspect() into
	// the switch above so we can make interleaved callbacks for
	// both Nodes and Tokens in the right order and avoid the need
	// to sort.
	sort.Sort(byPos(children))

	return children
}

type byPos []ast.Node

func (sl byPos) Len() int {
	return len(sl)
}
func (sl byPos) Less(i, j int) bool {
	return sl[i].Pos() < sl[j].Pos()
}
func (sl byPos) Swap(i, j int) {
	sl[i], sl[j] = sl[j], sl[i]
}
