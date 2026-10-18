// In-page overlay injected into every scene page: a visible mouse cursor that
// follows the real (Playwright-driven) pointer, a ripple on every click and a
// small key-cap badge for keyboard shortcuts. It is appended to <html>, not to
// <body>, so the app re-rendering its body never removes it.
(() => {
    if (window.__vc) return;

    const CURSOR_SVG =
        '<svg xmlns="http://www.w3.org/2000/svg" width="26" height="26" viewBox="0 0 26 26">' +
        '<path d="M3 2 L3 21 L8.2 16.4 L11.6 24 L15 22.5 L11.7 15 L18.6 15 Z" ' +
        'fill="#111" stroke="#fff" stroke-width="1.6" stroke-linejoin="round"/></svg>';

    const style = document.createElement('style');
    style.textContent = `
        #__vc-layer { position: fixed; inset: 0; pointer-events: none; z-index: 2147483647; }
        #__vc-cursor { position: absolute; left: 0; top: 0; width: 26px; height: 26px;
            transform: translate(-100px, -100px); filter: drop-shadow(0 1px 2px rgba(0,0,0,.35));
            transition: scale .12s ease-out; transform-origin: 3px 2px; }
        #__vc-cursor.down { scale: .86; }
        .__vc-ripple { position: absolute; width: 44px; height: 44px; margin: -22px 0 0 -22px;
            border-radius: 50%; background: rgba(255, 170, 0, .38); border: 2px solid rgba(255, 140, 0, .9);
            animation: __vc-ripple .65s ease-out forwards; }
        @keyframes __vc-ripple { from { transform: scale(.35); opacity: 1 } to { transform: scale(1.5); opacity: 0 } }
        .__vc-keys { position: absolute; display: flex; gap: 4px; padding: 0; transform: translate(18px, 22px);
            animation: __vc-keys 1.4s ease-out forwards; }
        .__vc-keys kbd { font: 600 13px/1 system-ui, sans-serif; color: #111; background: #fff;
            border: 1px solid #bbb; border-bottom-width: 3px; border-radius: 5px; padding: 5px 7px; }
        @keyframes __vc-keys { 0% { opacity: 0; translate: 0 4px } 12% { opacity: 1; translate: 0 0 }
            80% { opacity: 1 } 100% { opacity: 0 } }
    `;

    const layer = document.createElement('div');
    layer.id = '__vc-layer';
    const cursor = document.createElement('div');
    cursor.id = '__vc-cursor';
    cursor.innerHTML = CURSOR_SVG;
    layer.appendChild(cursor);

    let x = -100;
    let y = -100;
    const place = () => {
        cursor.style.transform = `translate(${x - 3}px, ${y - 2}px)`;
    };

    const mount = () => {
        document.documentElement.appendChild(style);
        document.documentElement.appendChild(layer);
    };
    if (document.documentElement) mount();
    else document.addEventListener('DOMContentLoaded', mount);

    window.addEventListener(
        'mousemove',
        (e) => {
            x = e.clientX;
            y = e.clientY;
            place();
        },
        true,
    );
    window.addEventListener('mousedown', () => {
        cursor.classList.add('down');
        const r = document.createElement('div');
        r.className = '__vc-ripple';
        r.style.left = `${x}px`;
        r.style.top = `${y}px`;
        layer.appendChild(r);
        setTimeout(() => r.remove(), 800);
    }, true);
    window.addEventListener('mouseup', () => cursor.classList.remove('down'), true);

    window.__vc = {
        /** Puts the cursor somewhere without an animation (a new page). */
        set(nx, ny) {
            x = nx;
            y = ny;
            place();
        },
        /** Shows a key-cap badge beside the cursor, e.g. keys('Ctrl', 'V'). */
        keys(...names) {
            const k = document.createElement('div');
            k.className = '__vc-keys';
            k.style.left = `${x}px`;
            k.style.top = `${y}px`;
            k.innerHTML = names.map((n) => `<kbd>${n}</kbd>`).join('');
            layer.appendChild(k);
            setTimeout(() => k.remove(), 1500);
        },
    };
})();
