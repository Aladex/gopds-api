import fs from 'node:fs';
import path from 'node:path';

import ts from 'typescript';

/**
 * Which files render a given component, answered by the compiler.
 *
 * The pager's column contract needs to know every page that renders it, and the
 * first two attempts at that read the source as text. The first asked whether a
 * file contained the characters `<BookPagination`, which recognised a spelling
 * rather than a component. The second parsed import statements with regular
 * expressions, and was wrong about `$Pager` (a `$` is an identifier character
 * and an anchor), about barrels (a file of exports is not one identity), about
 * `export *` (a star export carries no default), and about inline
 * `import { type X }`. Each round found another form of the language, because
 * deciding what a name denotes is not a job text matching can finish.
 *
 * TypeScript already does that job, and this package already depends on it. So
 * the question is put to a real program: resolve the module, take the symbol
 * its default export denotes, and ask of every identifier in every file which
 * symbol *it* denotes. Aliases are followed, so a name imported through a
 * barrel under a third spelling is still the same symbol; a different export of
 * the same barrel is a different symbol; a star export genuinely does not
 * expose a default; and a type-only import never stands in a value position.
 * The helper got smaller rather than larger, and the answer is the compiler's
 * rather than this file's.
 *
 * Two cases it still cannot decide, named rather than left silent:
 *
 *  - a dynamic import whose specifier is not a literal — `import(chosenLater)`
 *    resolves to nothing until it runs;
 *  - a namespace import reaching the default through a property access,
 *    `import * as M from …` and then `M.default`, which is a property lookup
 *    rather than an alias.
 *
 * Both are covered by the development-time assertion inside BookPagination,
 * which runs in the browser against the rendered tree, where neither the local
 * name nor the way it was imported exists any more.
 */

const SOURCE_EXTENSIONS = ['.tsx', '.ts', '.jsx', '.js'];
const SKIP_DIRECTORIES = new Set([
    'node_modules',
    'build',
    'dist',
    'coverage',
    'public',
    'placeholder',
    '.git',
    '__tests__',
]);

/** Every source file under `root` that could be a page, tests excluded. */
export function sourceFiles(root: string): string[] {
    const found: string[] = [];
    const walk = (dir: string) => {
        for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
            if (entry.isDirectory()) {
                if (!SKIP_DIRECTORIES.has(entry.name)) walk(path.join(dir, entry.name));
                continue;
            }
            const full = path.join(dir, entry.name);
            if (!SOURCE_EXTENSIONS.includes(path.extname(entry.name))) continue;
            if (/\.(test|spec)\.[cm]?[jt]sx?$/.test(entry.name)) continue;
            found.push(full);
        }
    };
    walk(root);
    return found;
}

/**
 * A program over those files, compiled with the project's own settings so that
 * `@/` and every other specifier resolves the way it does in the application.
 */
function programFor(root: string, rootNames: string[]): ts.Program {
    const configPath = path.join(root, 'tsconfig.json');
    const read = ts.readConfigFile(configPath, ts.sys.readFile);
    if (read.error) {
        const why = ts.flattenDiagnosticMessageText(read.error.messageText, ' ');
        throw new Error(`cannot read ${configPath}: ${why}`);
    }
    const parsed = ts.parseJsonConfigFileContent(read.config, ts.sys, root);
    if (parsed.errors.length > 0) {
        const why = parsed.errors
            .map((error) => ts.flattenDiagnosticMessageText(error.messageText, ' '))
            .join('; ');
        throw new Error(`cannot use ${configPath}: ${why}`);
    }
    return ts.createProgram({
        // The files walked above rather than the ones tsconfig includes: a page
        // living outside `src` is still a page, and the program pulls in
        // whatever those files import of its own accord.
        rootNames,
        options: { ...parsed.options, noEmit: true },
    });
}

/** What a name denotes, once every alias between here and there is followed. */
function denoted(checker: ts.TypeChecker, symbol: ts.Symbol): ts.Symbol {
    return symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol;
}

/** Whether a node stands inside a type, where nothing is ever rendered. */
function insideAType(node: ts.Node): boolean {
    for (let parent = node.parent; parent; parent = parent.parent) {
        if (ts.isTypeNode(parent)) return true;
        if (ts.isSourceFile(parent)) return false;
    }
    return false;
}

/**
 * discoverImporters lists every source file that renders `moduleFile`'s default
 * export, by whatever name and in whatever form.
 *
 * It throws rather than returning nothing when the module is not in the program
 * or exports no default: an inventory that can quietly answer "nobody" is an
 * inventory that passes when it is broken.
 *
 * Paths come back relative to `root`, sorted.
 */
export function discoverImporters(root: string, moduleFile: string): string[] {
    const candidates = sourceFiles(root);
    const program = programFor(root, candidates);
    const checker = program.getTypeChecker();

    const target = program.getSourceFile(path.resolve(moduleFile));
    if (!target) {
        throw new Error(`${moduleFile} is not part of the program rooted at ${root}`);
    }
    const moduleSymbol = checker.getSymbolAtLocation(target);
    if (!moduleSymbol) {
        throw new Error(`${moduleFile} is not a module — it exports nothing to look for`);
    }
    const exported = checker
        .getExportsOfModule(moduleSymbol)
        .find((symbol) => symbol.name === 'default');
    if (!exported) {
        throw new Error(`${moduleFile} has no default export to look for`);
    }
    // Followed, not taken as it stands. `export default BookPagination` at the
    // foot of a file makes the module's `default` an alias to the constant
    // above it, and an importing file's name resolves to that constant — so
    // comparing against the unfollowed export matches nothing at all.
    const component = denoted(checker, exported);

    const defaultExportOf = (specifier: ts.Node): ts.Symbol | undefined => {
        const imported = checker.getSymbolAtLocation(specifier);
        if (!imported) return undefined;
        return checker.getExportsOfModule(imported).find((exported) => exported.name === 'default');
    };

    const renders = (file: ts.SourceFile): boolean => {
        let found = false;
        const visit = (node: ts.Node) => {
            if (found) return;
            // An import or export statement binds names; it does not use them.
            // A file that only passes the component on — a barrel — is not the
            // page that renders it.
            if (
                ts.isImportDeclaration(node) ||
                ts.isExportDeclaration(node) ||
                ts.isImportEqualsDeclaration(node)
            ) {
                return;
            }
            if (
                ts.isCallExpression(node) &&
                node.expression.kind === ts.SyntaxKind.ImportKeyword &&
                node.arguments.length > 0 &&
                ts.isStringLiteralLike(node.arguments[0])
            ) {
                const lazily = defaultExportOf(node.arguments[0]);
                if (lazily && denoted(checker, lazily) === component) {
                    found = true;
                    return;
                }
            }
            if (ts.isIdentifier(node) && !insideAType(node)) {
                const symbol = checker.getSymbolAtLocation(node);
                if (symbol && denoted(checker, symbol) === component) {
                    found = true;
                    return;
                }
            }
            ts.forEachChild(node, visit);
        };
        ts.forEachChild(file, visit);
        return found;
    };

    const wanted = new Set(candidates.map((file) => path.resolve(file)));
    return program
        .getSourceFiles()
        .filter((file) => wanted.has(path.resolve(file.fileName)))
        .filter((file) => file !== target)
        .filter(renders)
        .map((file) => path.relative(root, file.fileName))
        .sort();
}
