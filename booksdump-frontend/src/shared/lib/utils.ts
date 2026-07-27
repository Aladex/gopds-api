import { type ClassValue, clsx } from 'clsx';
import { twMerge } from 'tailwind-merge';

/**
 * cn merges Tailwind class names, letting later classes win over earlier ones
 * even when both target the same property.
 */
export function cn(...inputs: ClassValue[]) {
    return twMerge(clsx(inputs));
}
