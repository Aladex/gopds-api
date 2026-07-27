import React from 'react';
import { useTranslation } from 'react-i18next';
import { Moon, Sun } from 'lucide-react';

import { Button } from '@/components/ui/button';

import { useTheme } from '@/context/ThemeContext';

const ThemeToggle: React.FC = () => {
    const { mode, toggleTheme } = useTheme();
    const { t } = useTranslation();
    const label = mode === 'light' ? t('switchToDark') : t('switchToLight');

    return (
        <Button
            variant="ghost"
            size="icon"
            onClick={toggleTheme}
            title={label}
            aria-label={label}
            className="text-white hover:bg-white/10 hover:text-white"
        >
            {mode === 'light' ? <Moon className="size-5" /> : <Sun className="size-5" />}
        </Button>
    );
};

export default ThemeToggle;
