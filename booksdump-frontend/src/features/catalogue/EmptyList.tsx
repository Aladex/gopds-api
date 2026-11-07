import React from 'react';

type EmptyListProps = {
    icon: React.ReactNode;
    title: React.ReactNode;
    /** What the reader can do about it, or why it is empty. */
    body: React.ReactNode;
    children?: React.ReactNode;
};

/** A list with nothing to show, saying why: the frame the author search's has. */
const EmptyList: React.FC<EmptyListProps> = ({ icon, title, body, children }) => (
    <div className="rounded border border-border bg-card px-5 py-8 text-center">
        <div className="mb-3 flex justify-center text-muted-foreground">{icon}</div>
        <h3 className="mt-0 mb-1.5 text-[17px] leading-[1.3] font-semibold">{title}</h3>
        <p className="mt-0 mb-0 text-sm text-muted-foreground">{body}</p>
        {children}
    </div>
);

export default EmptyList;
