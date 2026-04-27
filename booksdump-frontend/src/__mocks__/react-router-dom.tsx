// Manual mock for react-router-dom@7. Jest 27 (via react-scripts 5) does not
// resolve the package's exports map, so we provide minimal stand-ins for the
// hooks/components our public + admin tests actually use.

import React from 'react';

export const Link: React.FC<{ to?: string; children?: React.ReactNode } & Record<string, any>> = ({
    to,
    children,
    ...rest
}) => (
    <a href={typeof to === 'string' ? to : '#'} {...rest}>
        {children}
    </a>
);

// Tests that need a different :id can override useParams via jest.spyOn or by
// re-mocking this module locally with jest.mock(...) + factory.
export const useParams = (): Record<string, string | undefined> => ({});

export const useNavigate = () => () => {};
export const useLocation = () => ({ pathname: '/' });

export const MemoryRouter: React.FC<{ children?: React.ReactNode }> = ({ children }) => (
    <>{children}</>
);

export const Routes: React.FC<{ children?: React.ReactNode }> = ({ children }) => <>{children}</>;
export const Route: React.FC<{ children?: React.ReactNode }> = ({ children }) => <>{children}</>;
export const Navigate: React.FC = () => null;
