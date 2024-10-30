export function downloadViaIframe(url: string): void {
    const iframe = document.createElement('iframe');
    iframe.style.display = 'none';
    iframe.src = url;
    document.body.appendChild(iframe);

    iframe.onload = () => {
        setTimeout(() => document.body.removeChild(iframe), 1000);
    };
}
