import { TEX_H, TEX_W, typeset } from './typeset';
import { PLATE, STAMP_RING, type Passage } from './text';

/**
 * The printed sides of the sheets, drawn on 2D canvases and handed to WebGL as
 * textures. Paper is the scanned fibre toned to old book stock and aged at the
 * edges; on it, the print, a faint mirror image of the other side showing
 * through, and on some sheets a stamp, gold leaf or an engraving.
 */

/** Images the canvases draw from, loaded by the glue. */
export interface PrintSources {
    paper: HTMLImageElement;
    devito: HTMLImageElement;
    stamp: HTMLImageElement;
}

function canvas(w: number, h: number): [HTMLCanvasElement, CanvasRenderingContext2D] {
    const c = document.createElement('canvas');
    c.width = w;
    c.height = h;
    return [c, c.getContext('2d')!];
}

/**
 * A page canvas at `scale` of the full page texture, drawn in full-size page
 * units: everything below is laid out for 768 × 1086 and simply comes out
 * smaller. A phone draws its pages at half size — a quarter of the pixels to
 * typeset, upload and mipmap, on a screen where a page is a few hundred pixels
 * tall anyway.
 */
function page(scale: number): [HTMLCanvasElement, CanvasRenderingContext2D] {
    const [c, g] = canvas(Math.round(TEX_W * scale), Math.round(TEX_H * scale));
    g.scale(scale, scale);
    return [c, g];
}

/** Where the library stamp sits on the verso of the first passage: x, y, radius, rotation. */
export const STAMP = { x: 515, y: 830, r: 140, rot: -0.21 };

/**
 * The Leprosorium library stamp in red ink: the logo in the centre, lettering
 * on the ring. Drawn on its own canvas so the rubber's wear can be knocked out
 * of it before it is printed onto the page with multiply.
 */
function stampCanvas(r: number, logo: HTMLImageElement): HTMLCanvasElement {
    const size = Math.ceil(r * 2 + 16);
    const [c, g] = canvas(size, size);
    g.translate(size / 2, size / 2);
    g.fillStyle = g.strokeStyle = 'rgb(168,34,28)';
    g.lineWidth = r * 0.05;
    g.beginPath();
    g.arc(0, 0, r - g.lineWidth / 2, 0, Math.PI * 2);
    g.stroke();
    g.lineWidth = r * 0.018;
    g.beginPath();
    g.arc(0, 0, r * 0.86, 0, Math.PI * 2);
    g.stroke();
    g.beginPath();
    g.arc(0, 0, r * 0.66, 0, Math.PI * 2);
    g.stroke();
    g.font = `bold ${Math.round(r * 0.13)}px "PT Serif"`;
    g.textAlign = 'center';
    g.textBaseline = 'middle';
    for (let i = 0; i < STAMP_RING.length; i++) {
        g.save();
        g.rotate((i / STAMP_RING.length) * Math.PI * 2);
        g.fillText(STAMP_RING[i], 0, -r * 0.76);
        g.restore();
    }
    // Centre: the real one-colour logo, inked in the stamp's red.
    const lw = r * 1.18;
    const lh = (lw * logo.height) / logo.width;
    const [ink, ig] = canvas(Math.ceil(lw), Math.ceil(lh));
    ig.drawImage(logo, 0, 0, ink.width, ink.height);
    ig.globalCompositeOperation = 'source-in';
    ig.fillStyle = g.fillStyle;
    ig.fillRect(0, 0, ink.width, ink.height);
    g.drawImage(ink, -lw / 2, -lh / 2, lw, lh);
    // Wear: the rubber never inks evenly, and one side was pressed harder.
    g.globalCompositeOperation = 'destination-out';
    for (let i = 0; i < 900; i++) {
        g.globalAlpha = Math.random() * 0.8;
        g.beginPath();
        g.arc(
            (Math.random() - 0.5) * size,
            (Math.random() - 0.5) * size,
            Math.random() * r * 0.025,
            0,
            Math.PI * 2,
        );
        g.fill();
    }
    const fade = g.createLinearGradient(-r, -r, r, r);
    fade.addColorStop(0, 'rgba(0,0,0,0)');
    fade.addColorStop(1, 'rgba(0,0,0,0.45)');
    g.globalAlpha = 1;
    g.fillStyle = fade;
    g.fillRect(-size, -size, size * 2, size * 2);
    return c;
}

/** Width of the gold leaf along a gilt sheet's edges, in texture pixels. */
const GILT = 38;

/**
 * Metalness and roughness maps for a gilt-edged sheet: gold leaf on the edges,
 * plain paper inside. The shine comes from these, not from the colour.
 */
export function giltMaps(scale = 1): { metal: HTMLCanvasElement; rough: HTMLCanvasElement } {
    const make = (inside: string, edge: string) => {
        const [c, g] = page(scale);
        g.fillStyle = edge;
        g.fillRect(0, 0, TEX_W, TEX_H);
        g.fillStyle = inside;
        g.fillRect(GILT, GILT, TEX_W - GILT * 2, TEX_H - GILT * 2);
        return c;
    };
    return { metal: make('#000', '#fff'), rough: make('#f2f2f2', '#404040') };
}

/** The engraving plate: a full-page illustration with a caption, as in an old illustrated edition. */
function plateBody(g: CanvasRenderingContext2D, devito: HTMLImageElement): void {
    g.textBaseline = 'alphabetic';
    g.globalAlpha = 0.9;
    g.font = 'italic 20px "PT Serif"';
    g.textAlign = 'center';
    g.fillText(PLATE.runningHead, TEX_W / 2, 62);
    g.font = '20px "PT Serif"';
    g.fillText(PLATE.number, TEX_W / 2, 200);
    const w = TEX_W - 160;
    const h = (w * devito.height) / devito.width;
    const x = 80;
    const y = 230;
    g.save();
    g.filter = 'grayscale(1) contrast(1.2) brightness(0.95)';
    g.globalAlpha = 0.92;
    g.drawImage(devito, x, y, w, h);
    g.restore();
    g.globalAlpha = 0.8;
    g.lineWidth = 2;
    g.strokeRect(x - 12, y - 12, w + 24, h + 24);
    g.lineWidth = 0.8;
    g.strokeRect(x - 18, y - 18, w + 36, h + 36);
    g.globalAlpha = 0.9;
    g.font = 'italic 27px "PT Serif"';
    g.fillText(PLATE.caption, TEX_W / 2, y + h + 80);
    g.font = 'italic 21px "PT Serif"';
    g.fillText(PLATE.subcaption, TEX_W / 2, y + h + 118);
    g.textAlign = 'left';
    g.globalAlpha = 1;
}

export interface SideOptions {
    /** Print the plate instead of text; with `blank`, print nothing. */
    plate?: boolean;
    blank?: boolean;
    /** Print the library stamp. */
    stamp?: boolean;
    /** Gold leaf on the edges. */
    gilt?: boolean;
}

/**
 * One printed side of a sheet. `isBack` picks the verso content; `mirror`
 * flips it horizontally, so a back face reads correctly when seen through the
 * sheet's BackSide.
 */
export function paperCanvas(
    src: PrintSources,
    passage: Passage,
    folio: number,
    isBack: boolean,
    mirror: boolean,
    opts: SideOptions = {},
    scale = 1,
): HTMLCanvasElement {
    const [c, g] = page(scale);
    if (mirror) {
        g.translate(TEX_W, 0);
        g.scale(-1, 1);
    }

    // Real fibre scan, toned to old book stock. The scan is landscape; turn it
    // to run along the portrait page.
    g.save();
    g.translate(TEX_W / 2, TEX_H / 2);
    g.rotate(Math.PI / 2);
    g.drawImage(src.paper, -TEX_H / 2, -TEX_W / 2, TEX_H, TEX_W);
    g.restore();
    g.globalCompositeOperation = 'multiply';
    g.fillStyle = '#eddcbb';
    g.fillRect(0, 0, TEX_W, TEX_H);

    // Ageing: darker toward the edges, a toned band along them, a few foxing spots.
    const vg = g.createRadialGradient(
        TEX_W / 2,
        TEX_H / 2,
        TEX_H * 0.3,
        TEX_W / 2,
        TEX_H / 2,
        TEX_H * 0.78,
    );
    vg.addColorStop(0, 'rgba(255,255,255,0)');
    vg.addColorStop(1, 'rgba(150,110,65,0.35)');
    g.fillStyle = vg;
    g.fillRect(0, 0, TEX_W, TEX_H);
    const band = 26;
    const bands: [number, number, number, number, number, number, number, number][] = [
        [0, 0, band, 0, 0, 0, band, TEX_H],
        [TEX_W, 0, TEX_W - band, 0, TEX_W - band, 0, band, TEX_H],
        [0, 0, 0, band, 0, 0, TEX_W, band],
        [0, TEX_H, 0, TEX_H - band, 0, TEX_H - band, TEX_W, band],
    ];
    for (const [x0, y0, x1, y1, rx, ry, rw, rh] of bands) {
        const lg = g.createLinearGradient(x0, y0, x1, y1);
        lg.addColorStop(0, 'rgba(160,115,60,0.35)');
        lg.addColorStop(1, 'rgba(160,115,60,0)');
        g.fillStyle = lg;
        g.fillRect(rx, ry, rw, rh);
    }
    for (let i = 0; i < 12; i++) {
        const x = Math.random() * TEX_W;
        const y = Math.random() * TEX_H;
        const r = 3 + Math.random() * 22;
        const rg = g.createRadialGradient(x, y, 0, x, y, r);
        rg.addColorStop(0, `rgba(140,95,50,${0.05 + Math.random() * 0.1})`);
        rg.addColorStop(1, 'rgba(140,95,50,0)');
        g.fillStyle = rg;
        g.fillRect(x - r, y - r, r * 2, r * 2);
    }

    // Show-through: the print on the other side, mirrored, faintly visible.
    // Plates were printed on one side only, so nothing shows through them.
    if (!opts.plate) {
        const [st, sg] = page(scale);
        sg.fillStyle = 'rgb(60,45,32)';
        typeset(sg, passage, folio, !isBack);
        g.save();
        g.globalAlpha = 0.09;
        g.translate(TEX_W, 0);
        g.scale(-1, 1);
        g.drawImage(st, 0, 0, TEX_W, TEX_H);
        g.restore();
    }

    // Ink, with a hair of spread into the fibres.
    g.fillStyle = 'rgb(30,22,16)';
    g.shadowColor = 'rgba(30,22,16,0.35)';
    g.shadowBlur = 1.2;
    if (opts.plate) {
        if (!opts.blank) plateBody(g, src.devito);
    } else typeset(g, passage, folio, isBack);
    g.shadowBlur = 0;

    if (opts.stamp) {
        const stamp = stampCanvas(STAMP.r, src.stamp);
        g.save();
        g.translate(STAMP.x, STAMP.y);
        g.rotate(STAMP.rot);
        g.globalAlpha = 0.82;
        g.filter = 'blur(0.6px)';
        g.drawImage(stamp, -stamp.width / 2, -stamp.height / 2);
        g.restore();
    }
    g.globalCompositeOperation = 'source-over';

    if (opts.gilt) {
        // Gold leaf: a warm gradient with burnish streaks.
        const gold = g.createLinearGradient(0, 0, TEX_W, TEX_H);
        gold.addColorStop(0, '#e2c06a');
        gold.addColorStop(0.5, '#b8923e');
        gold.addColorStop(1, '#d9b45c');
        g.fillStyle = gold;
        g.fillRect(0, 0, TEX_W, GILT);
        g.fillRect(0, TEX_H - GILT, TEX_W, GILT);
        g.fillRect(0, 0, GILT, TEX_H);
        g.fillRect(TEX_W - GILT, 0, GILT, TEX_H);
        g.globalAlpha = 0.25;
        for (let i = 0; i < 300; i++) {
            g.fillStyle = Math.random() < 0.5 ? '#fff1c4' : '#7a5a20';
            const along = Math.random() < 0.5;
            g.fillRect(
                along ? Math.random() * TEX_W : Math.random() * GILT,
                along ? Math.random() * GILT : Math.random() * TEX_H,
                along ? 30 : 1,
                along ? 1 : 30,
            );
        }
        g.globalAlpha = 1;
    }
    return c;
}

/** Fore-edge of a page block: hundreds of sheet edges, each a slightly different shade. */
export function edgeCanvas(): HTMLCanvasElement {
    const [c, g] = canvas(512, 4);
    for (let x = 0; x < 512; x++) {
        const v = 190 + Math.random() * 40;
        g.fillStyle = `rgb(${v + 14},${v},${v - 30})`;
        g.fillRect(x, 0, 1, 4);
    }
    return c;
}
