import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { discoverImporters } from '@/__tests__/discoverImporters';

/*
 * The pager's column contract is only as good as its inventory of callers, and
 * the first inventory asked whether a file contained the characters
 * `<BookPagination`. Four render forms were put to that regex and three of them
 * came back false — an aliased default import, React.createElement, and a lazy
 * import bound to another name — so three ways of rendering the component
 * walked past the entire suite.
 *
 * These tests hold the replacement to the property that failed: a file that
 * reaches the module is found whatever the local binding is called and whatever
 * it does with it. They run against a tree written here rather than the
 * project's own, so the forms can be stated one at a time and the awkward ones
 * — a barrel, a type-only import, a name imported and never used — can be
 * stated at all.
 */

let root: string;

const write = (relative: string, source: string) => {
    const full = path.join(root, relative);
    fs.mkdirSync(path.dirname(full), { recursive: true });
    fs.writeFileSync(full, source);
};

const importersOfPager = () =>
    discoverImporters(root, path.join(root, 'src/features/catalogue/BookPagination.tsx'));

beforeEach(() => {
    root = fs.mkdtempSync(path.join(os.tmpdir(), 'importers-'));
    // Each fixture is a miniature of this project: the same alias, the same
    // JSX setting, so the specifiers below resolve the way the real ones do.
    write(
        'tsconfig.json',
        JSON.stringify({
            compilerOptions: {
                target: 'ES2022',
                module: 'ESNext',
                moduleResolution: 'bundler',
                jsx: 'react-jsx',
                allowJs: true,
                strict: false,
                noEmit: true,
                baseUrl: '.',
                paths: { '@/*': ['./src/*'] },
            },
            include: ['src'],
        }),
    );
    // Shaped like the component it stands for: declared, then exported at the
    // foot of the file. That makes the module's `default` an alias to a local
    // constant rather than the declaration itself, and a discovery that
    // compares against the export without following it finds nobody at all.
    write(
        'src/features/catalogue/BookPagination.tsx',
        `const BookPagination = () => null;
         export default BookPagination;`,
    );
});

afterEach(() => {
    fs.rmSync(root, { recursive: true, force: true });
});

describe('discoverImporters', () => {
    it('finds a plain JSX caller', () => {
        write(
            'src/pages/Plain.tsx',
            `import BookPagination from '@/features/catalogue/BookPagination';
             export default () => <BookPagination totalPages={9} />;`,
        );

        expect(importersOfPager()).toEqual(['src/pages/Plain.tsx']);
    });

    it('finds one that renamed it on the way in', () => {
        // The form the old regex missed, and the one a new page is most likely
        // to be written in: nothing about the local name says which component
        // it is.
        write(
            'src/pages/Aliased.tsx',
            `import Pager from '@/features/catalogue/BookPagination';
             export default () => <Pager totalPages={9} />;`,
        );

        expect(importersOfPager()).toEqual(['src/pages/Aliased.tsx']);
    });

    it('finds a caller of a component exported where it is declared', () => {
        // The other of the two shapes a default export comes in. Every fixture
        // here uses the indirect one, because that is what the component being
        // guarded does; this keeps the direct one from quietly stopping to work.
        write(
            'src/features/catalogue/BookPagination.tsx',
            'export default function BookPagination() { return null; }',
        );
        write(
            'src/pages/Direct.tsx',
            `import Pager from '@/features/catalogue/BookPagination';
             export default () => <Pager totalPages={9} />;`,
        );

        expect(importersOfPager()).toEqual(['src/pages/Direct.tsx']);
    });

    it('finds one whose local name a regular expression would mistake for an anchor', () => {
        // `$` is a legal character in a JavaScript identifier and an anchor in
        // a regular expression, so a discovery built out of `new RegExp` reads
        // `$Pager` as "end of input followed by Pager" and finds nothing. The
        // name is arbitrary; that is the point — nothing about a local name
        // should decide whether a page is answerable for its column.
        write(
            'src/pages/Dollar.tsx',
            `import React from 'react';
             import $Pager from '@/features/catalogue/BookPagination';
             export default () => React.createElement($Pager, { totalPages: 9 });`,
        );

        expect(importersOfPager()).toEqual(['src/pages/Dollar.tsx']);
    });

    it('leaves out a page that took a different symbol from the same barrel', () => {
        // A barrel is a file of exports, not one identity. Treating the whole
        // file as "the pager" makes every consumer of anything it re-exports a
        // pager caller, and then an ordinary refactor demands a column and a
        // render case from a page that has no pager on it.
        write('src/features/catalogue/Other.tsx', 'export default function Other() {}\n');
        write(
            'src/features/catalogue/index.ts',
            `export { default as Pager } from './BookPagination';
             export { default as Other } from './Other';`,
        );
        write(
            'src/pages/OtherPage.tsx',
            `import { Other } from '@/features/catalogue';
             export default () => <Other />;`,
        );

        expect(importersOfPager()).toEqual([]);
    });

    it('knows a star export carries no default', () => {
        // `export *` re-exports the named exports and never the default, so a
        // barrel written that way cannot hand the component on, and a page
        // taking a named helper through it is not a caller.
        write(
            'src/features/catalogue/BookPagination.tsx',
            `export const pageHref = (page: number) => \`/books/page/\${page}\`;
             const BookPagination = () => null;
             export default BookPagination;`,
        );
        write('src/features/catalogue/index.ts', `export * from './BookPagination';`);
        write(
            'src/pages/HelperPage.tsx',
            `import { pageHref } from '@/features/catalogue';
             export default () => <a href={pageHref(2)}>next</a>;`,
        );

        expect(importersOfPager()).toEqual([]);
    });

    it('leaves out a file whose type import is marked inline', () => {
        // `import { type X }` is the same promise as `import type { X }` and
        // renders just as little.
        write(
            'src/features/catalogue/BookPagination.tsx',
            `export interface PagerProps { totalPages: number }
             const BookPagination = () => null;
             export default BookPagination;`,
        );
        write(
            'src/pages/InlineType.tsx',
            `import { type PagerProps } from '@/features/catalogue/BookPagination';
             export const widen = (props: PagerProps) => props.totalPages;`,
        );

        expect(importersOfPager()).toEqual([]);
    });

    it('finds one that renders it without JSX', () => {
        write(
            'src/pages/Created.tsx',
            `import React from 'react';
             import BookPagination from '@/features/catalogue/BookPagination';
             export default () => React.createElement(BookPagination, { totalPages: 9 });`,
        );

        expect(importersOfPager()).toEqual(['src/pages/Created.tsx']);
    });

    it('finds one that loads it lazily', () => {
        // The specifier is written down even though the binding is not, and
        // reaching the module is what makes a file answerable for the column.
        write(
            'src/pages/Lazy.tsx',
            `import { lazy } from 'react';
             const Anything = lazy(() => import('@/features/catalogue/BookPagination'));
             export default () => <Anything totalPages={9} />;`,
        );

        expect(importersOfPager()).toEqual(['src/pages/Lazy.tsx']);
    });

    it('follows a relative specifier as readily as the alias', () => {
        write(
            'src/features/catalogue/Neighbour.tsx',
            `import Pager from './BookPagination';
             export default () => <Pager totalPages={9} />;`,
        );

        expect(importersOfPager()).toEqual(['src/features/catalogue/Neighbour.tsx']);
    });

    it('looks inside .jsx as well, which this project still compiles', () => {
        // tsconfig sets allowJs, so a .jsx page is a page the contract binds.
        write(
            'src/pages/Untyped.jsx',
            `import Pager from '@/features/catalogue/BookPagination';
             export default () => <Pager totalPages={9} />;`,
        );

        expect(importersOfPager()).toEqual(['src/pages/Untyped.jsx']);
    });

    it('names the page rather than the barrel it came through', () => {
        // A re-export passes the module on; it does not render it, and making
        // it answer for a column it does not own would only teach the next
        // reader to add an exception.
        write(
            'src/features/catalogue/index.ts',
            `export { default } from '@/features/catalogue/BookPagination';`,
        );
        write(
            'src/pages/ViaBarrel.tsx',
            `import Whatever from '@/features/catalogue';
             export default () => <Whatever totalPages={9} />;`,
        );

        expect(importersOfPager()).toEqual(['src/pages/ViaBarrel.tsx']);
    });

    it('leaves out a file that imports the type and renders nothing', () => {
        write(
            'src/pages/TypeOnly.tsx',
            `import type BookPagination from '@/features/catalogue/BookPagination';
             export type Props = { pager: typeof BookPagination };`,
        );

        expect(importersOfPager()).toEqual([]);
    });

    it('leaves out a file that imports it and never mentions it again', () => {
        write(
            'src/pages/Unused.tsx',
            `import BookPagination from '@/features/catalogue/BookPagination';
             export default () => <p>no pager here</p>;`,
        );

        expect(importersOfPager()).toEqual([]);
    });

    it('leaves the tests out of it, wherever they are kept', () => {
        // A suite that rendered the pager would otherwise have to mark a column
        // of its own, which is a contract about production pages. Both of this
        // project's conventions are covered: a __tests__ directory, and a
        // suite sitting beside the file it exercises.
        write(
            'src/pages/__tests__/Something.test.tsx',
            `import BookPagination from '@/features/catalogue/BookPagination';
             it('renders', () => render(<BookPagination totalPages={9} />));`,
        );
        write(
            'src/pages/Beside.test.tsx',
            `import BookPagination from '@/features/catalogue/BookPagination';
             it('renders', () => render(<BookPagination totalPages={9} />));`,
        );
        // And the support code beside them, which carries no .test in its name
        // and is still nobody's page.
        write(
            'src/pages/__tests__/renderPager.tsx',
            `import Pager from '@/features/catalogue/BookPagination';
             export const renderPager = () => render(<Pager totalPages={9} />);`,
        );

        expect(importersOfPager()).toEqual([]);
    });

    it('finds every one of them at once', () => {
        write(
            'src/pages/Plain.tsx',
            `import BookPagination from '@/features/catalogue/BookPagination';
             export default () => <BookPagination totalPages={9} />;`,
        );
        write(
            'src/pages/Aliased.tsx',
            `import Pager from '@/features/catalogue/BookPagination';
             export default () => <Pager totalPages={9} />;`,
        );
        write(
            'src/pages/Lazy.tsx',
            `const Anything = lazy(() => import('@/features/catalogue/BookPagination'));
             export default () => <Anything totalPages={9} />;`,
        );

        expect(importersOfPager()).toEqual([
            'src/pages/Aliased.tsx',
            'src/pages/Lazy.tsx',
            'src/pages/Plain.tsx',
        ]);
    });
});
