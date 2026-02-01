import React from 'react';
import { IconButton, Tooltip } from '@mui/material';
import { Brightness4, Brightness7 } from '@mui/icons-material';
import { useTranslation } from 'react-i18next';
import { useTheme } from '../../context/ThemeContext';

const ThemeToggle: React.FC = () => {
    const { mode, toggleTheme } = useTheme();
    const { t } = useTranslation();

    return (
        <Tooltip title={mode === 'light' ? t('switchToDark') : t('switchToLight')}>
            <IconButton
                onClick={toggleTheme}
                sx={{ color: 'common.white' }}
            >
                {mode === 'light' ? <Brightness4 /> : <Brightness7 />}
            </IconButton>
        </Tooltip>
    );
};

export default ThemeToggle;
