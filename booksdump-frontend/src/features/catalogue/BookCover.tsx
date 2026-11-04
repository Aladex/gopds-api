import React from 'react';

import { BouncingDots } from '@/shared/ui/bouncing-dots';
import { cn } from '@/shared/lib/utils';

/**
 * BookCover holds the space a cover will occupy and says it is coming.
 *
 * A list of ten books is ten separate requests for pictures that arrive in no
 * particular order, and an <img> with nothing behind it is a blank rectangle
 * until each one lands. The dots are that rectangle admitting it is waiting.
 *
 * The frame is sized by the caller and never by the image, so nothing on the
 * page moves when a cover finally arrives.
 *
 * A book with no cover, or whose cover fails to load, gets a typographic tile
 * instead: its title and first author on a muted tile under a band of the
 * primary colour. A generic placeholder said only that there was no picture;
 * the tile says which book it is. The text sits at the bottom, so the corner
 * the favourite star takes stays clear.
 *
 * Whatever the caller lays over the cover (the star) goes in as children.
 */

export interface BookCoverProps {
    src: string;
    alt: string;
    /** Whether the book has a cover at all; without one no picture is asked for. */
    hasPicture: boolean;
    /** What the tile says when there is no picture. */
    title: string;
    author?: string;
    className?: string;
    children?: React.ReactNode;
}

/*
 * The tile's words are drawn by CSS from data attributes rather than written
 * into the document. They repeat the card's own heading and byline, and as
 * text they would be found twice by the browser's find-in-page, copied twice
 * with a selection, and matched twice by anything that looks for the title.
 * Decoration, so hidden from assistive technology as well.
 */
const TypographicCover: React.FC<{ title: string; author?: string }> = ({ title, author }) => (
    <div
        aria-hidden="true"
        data-testid="book-cover-tile"
        className={cn(
            'flex size-full flex-col justify-end gap-1 border-t-[3px] border-primary bg-secondary text-foreground',
            'px-1.5 pt-8 pb-2 card:px-2 card:pt-10 card:pb-2.5',
        )}
    >
        <span
            data-tile-title={title}
            className="line-clamp-3 text-[10px] leading-tight font-semibold hyphens-auto [overflow-wrap:anywhere] before:content-[attr(data-tile-title)] card:line-clamp-4 card:text-xs"
        />
        {author && (
            <span
                data-tile-author={author}
                className="line-clamp-2 text-[9.5px] leading-tight text-muted-foreground before:content-[attr(data-tile-author)] card:text-[11px]"
            />
        )}
    </div>
);

type CoverPictureProps = Pick<BookCoverProps, 'src' | 'alt' | 'hasPicture' | 'title' | 'author'>;

/**
 * CoverPicture is the inside of the frame: the picture on its way, the
 * picture, or the tile. Its state belongs to one address — BookCover keys it
 * by the address and by whether there is a picture at all, so a new poster
 * after an edit or a rescan starts over: asked for, waited for, and shown,
 * rather than kept behind the tile by the old address's failure.
 */
const CoverPicture: React.FC<CoverPictureProps> = ({ src, alt, hasPicture, title, author }) => {
    const imgRef = React.useRef<HTMLImageElement | null>(null);
    const [settled, setSettled] = React.useState(false);
    const [failed, setFailed] = React.useState(false);

    // A cached image can finish before React has attached onLoad, and then the
    // event never comes and the dots hop forever. The element itself knows.
    React.useEffect(() => {
        if (imgRef.current?.complete) {
            setSettled(true);
        }
    }, []);

    if (!hasPicture || failed) return <TypographicCover title={title} author={author} />;

    return (
        <>
            {!settled && (
                <span className="absolute inset-0 flex items-center justify-center text-muted-foreground">
                    <BouncingDots />
                </span>
            )}
            <img
                ref={imgRef}
                src={src}
                alt={alt}
                loading="lazy"
                onLoad={() => setSettled(true)}
                // A cover that fails is replaced by the tile: leaving the
                // dots, or a broken image, on it would be a lie.
                onError={() => setFailed(true)}
                className={cn(
                    'size-full object-cover transition-opacity duration-200',
                    settled ? 'opacity-100' : 'opacity-0',
                )}
            />
        </>
    );
};

const BookCover: React.FC<BookCoverProps> = ({
    src,
    alt,
    hasPicture,
    title,
    author,
    className,
    children,
}) => (
    // The frame clips the picture; what is laid over it (the star) sits
    // outside the clip, so its hit area may reach past the cover's edge.
    <div data-testid="book-cover" className={cn('relative', className)}>
        <div className="size-full overflow-hidden rounded-[inherit] bg-muted">
            <CoverPicture
                key={`${hasPicture}:${src}`}
                src={src}
                alt={alt}
                hasPicture={hasPicture}
                title={title}
                author={author}
            />
        </div>
        {children}
    </div>
);

export default BookCover;
