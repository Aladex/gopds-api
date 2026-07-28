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
 */

export interface BookCoverProps {
    src: string;
    alt: string;
    className?: string;
}

const BookCover: React.FC<BookCoverProps> = ({ src, alt, className }) => {
    const imgRef = React.useRef<HTMLImageElement | null>(null);
    const [settled, setSettled] = React.useState(false);

    // A cached image can finish before React has attached onLoad, and then the
    // event never comes and the dots hop forever. The element itself knows.
    React.useEffect(() => {
        if (imgRef.current?.complete) {
            setSettled(true);
        }
    }, [src]);

    return (
        <div className={cn('relative overflow-hidden bg-muted', className)}>
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
                // Both, because a cover that fails is as done as one that
                // arrives — leaving the dots on a broken image is a lie.
                onLoad={() => setSettled(true)}
                onError={() => setSettled(true)}
                className={cn(
                    'size-full object-cover transition-opacity duration-200',
                    settled ? 'opacity-100' : 'opacity-0',
                )}
            />
        </div>
    );
};

export default BookCover;
