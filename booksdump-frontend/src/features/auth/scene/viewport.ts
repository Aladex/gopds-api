/**
 * Portrait phones get their own composition rather than a squeezed desktop
 * one: a wider lens, the objects pulled in toward the centre, and a lighter
 * pipeline — fewer sheets, smaller shadow maps, no ambient occlusion or depth
 * of field — because the same frame on a phone GPU costs several times more.
 *
 * On its own, without three.js, because the screens ask it before the scene
 * chunk is loaded and must not pull the library in to do so.
 */
export function isPhoneViewport(width: number, height: number): boolean {
    return width / height < 0.8;
}
