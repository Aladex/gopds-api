import React from 'react';
import { Typography, Box } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useSearchBar } from '../../context/SearchBarContext';
import { buttonLinkSx } from '../commonStyles';

interface Genre {
    id: number;
    genre: string;
}

interface GenresListProps {
    genres: Genre[];
}

const GenresList: React.FC<GenresListProps> = ({ genres }) => {
    const navigate = useNavigate();
    const { setSearchItem } = useSearchBar();
    const { t } = useTranslation();
    const navigateToGenre = (genreId: number) => {
        setSearchItem('');
        navigate(`/books/find/genre/${genreId}/1`);
    };

    if (!genres || genres.length === 0) {
        return null;
    }

    return (
        <div>
            <Typography variant="subtitle1">{t('genres')}:</Typography>
            <Typography variant="body2" sx={{ color: 'text.primary' }}>
                {genres.map((genre: Genre) => (
                    <React.Fragment key={genre.id}>
                        <span> &#8226; </span>
                        <Box
                            component="button"
                            onClick={() => navigateToGenre(genre.id)}
                            sx={(theme) => ({
                                ...buttonLinkSx(theme),
                                color: theme.palette.text.primary,
                            })}
                        >
                            {genre.genre}
                        </Box>
                    </React.Fragment>
                ))}
            </Typography>
        </div>
    );
};

export default GenresList;
