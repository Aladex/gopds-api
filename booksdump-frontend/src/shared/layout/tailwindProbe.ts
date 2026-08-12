import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { __unstable__loadDesignSystem } from 'tailwindcss';

/**
 * The project's own Tailwind, compiled in process from src/index.css.
 *
 * Tests that want to know what a class *does* have to ask the compiler, not
 * read the class name: `text-[18px]` and `text-lg` are the same size and
 * different strings, and a string comparison answers for the spelling rather
 * than the typography. Reading a previous build instead would answer for
 * whatever was built last, and there is nothing to build on a clean checkout.
 *
 * Test-only, but it lives beside the source it compiles rather than under a
 * __tests__ directory: importing across feature folders to reach a helper is
 * worse than one small module next to the stylesheet it loads.
 */
const projectPath = (relative: string) => fileURLToPath(new URL(relative, import.meta.url));

let cached: Awaited<ReturnType<typeof __unstable__loadDesignSystem>> | undefined;

export const designSystem = async () => {
    cached ??= await __unstable__loadDesignSystem(
        readFileSync(projectPath('../../index.css'), 'utf8'),
        {
            base: projectPath('../../..'),
            loadStylesheet: async (id: string, base: string) => {
                const path =
                    id === 'tailwindcss'
                        ? projectPath('../../../node_modules/tailwindcss/index.css')
                        : '';
                return { path, base, content: path ? readFileSync(path, 'utf8') : '' };
            },
        },
    );
    return cached;
};

/**
 * The CSS a space-separated class list compiles to, as one string. Classes
 * Tailwind does not recognise contribute nothing, which is what lets a caller
 * pass a real className straight from the component.
 */
export const compileClasses = async (classes: string) => {
    const design = await designSystem();
    return design.candidatesToCss(classes.split(/\s+/).filter(Boolean)).filter(Boolean).join('\n');
};

export interface CompiledRule {
    candidate: string;
    /** Everything before the first `{` — the selector the rule applies to. */
    selector: string;
    /** The declarations, as written. */
    body: string;
}

/**
 * The class list compiled one candidate at a time, so a caller can ask not
 * only *whether* a declaration exists but *what it applies to*. Compiling the
 * whole list into one string cannot answer the second question, and that is
 * the difference between "the column is 62 characters wide" and "something in
 * here is 62 characters wide".
 */
export const compileRules = async (classes: string): Promise<CompiledRule[]> => {
    const design = await designSystem();
    const rules: CompiledRule[] = [];
    for (const candidate of classes.split(/\s+/).filter(Boolean)) {
        const [css] = design.candidatesToCss([candidate]);
        if (!css) continue;
        const brace = css.indexOf('{');
        if (brace === -1) continue;
        rules.push({
            candidate,
            selector: css.slice(0, brace).trim(),
            body: css.slice(brace + 1, css.lastIndexOf('}')).trim(),
        });
    }
    return rules;
};
