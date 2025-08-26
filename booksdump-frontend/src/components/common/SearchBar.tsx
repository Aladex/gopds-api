import React from 'react';
import {
    Box,
    Grid,
    Card,
    CardContent,
    Button,
    Select,
    MenuItem,
    InputLabel,
    IconButton
} from '@mui/material';
import { StyledTextField } from "../StyledDataItems";
import { useTranslation } from 'react-i18next';
import { useAuth } from "../../context/AuthContext";
import { Clear, Favorite, FavoriteBorder } from "@mui/icons-material";
import { useNavigate } from 'react-router-dom';
import { useFav } from "../../context/FavContext";
import { useAuthor } from "../../context/AuthorContext";
import { useSearchBar } from "../../context/SearchBarContext";
import { StyledFormControl} from "../StyledDataItems";
import useSearchOptions from "../hooks/useSearchOptions";
import { getLanguageDisplaySafe } from "../../utils/languageUtils";

interface Record {
    option: string;
    path: string;
}

const SearchBar: React.FC = () => {
    const { updateLang } = useAuth();
    const { t } = useTranslation();
    const { languages, selectedLanguage, searchItem, setSearchItem, selectedSearch, setSelectedSearch, setSelectedLanguage } = useSearchBar();
    const navigate = useNavigate();
    const { fav, favEnabled, setFav } = useFav();
    const searchOptions = useSearchOptions(setSelectedSearch);

    // Filter languages to only show those that are supported and have proper display
    const supportedLanguages = languages.filter(lang => getLanguageDisplaySafe(lang) !== null);

    const records: Record[] = [
        { option: 'authorsBookSearch', path: `/books/find/author/` },
        { option: 'title', path: `/books/find/title/${searchItem}/1` },
        { option: 'author', path: `/authors/${searchItem}/1` },
    ];
    const {authorId, setAuthorBook, clearAuthorId, clearAuthorBook } = useAuthor();

    const handleSetAuthorBook = () => {
        setAuthorBook(searchItem);
    };

    const setFavContext = (fav: boolean) => {
        setFav(fav);
    }

    const handleClear = () => {
        setSearchItem('');
        // Clear authorId in AuthorContext
        clearAuthorId();
        // Clear authorBook in AuthorContext
        clearAuthorBook();
    }

    const navigateToSearchResults = () => {
        const record = records.find(record => record.option === selectedSearch);
        handleSetAuthorBook();
        if (record) {
            // If option is 'authorsBookSearch', set authorId in AuthorContext, else clear it
            if (record.option !== 'authorsBookSearch') {
                clearAuthorId();
                clearAuthorBook();
                navigate(record.path);
            } else {
                navigate(record.path + authorId + '/1');
            }
        }
    };

    const updateLangAndSelectedLanguage = (lang: string) => {
        updateLang(lang);
        setSelectedLanguage(lang);
    };

    return (
        <Grid container justifyContent="center" spacing={4}>
            <Grid item xs={12}>
                <Box maxWidth={1200} mx="auto">
                    <Card sx={{ boxShadow: 2, p: 1, my: 1 }}>
                        <CardContent>
                            <Grid container spacing={2} justifyContent="start">
                                <Grid item xs={12} lg={6}>
                                    <Grid container spacing={2}>
                                        <Grid item xs={12} lg={6}>
                                            <StyledFormControl
                                                fullWidth
                                                sx={{
                                                    '& .MuiOutlinedInput-root': {
                                                        '& fieldset': {
                                                            borderColor: 'rgba(0, 0, 0, 0.23)',
                                                        },
                                                        '&:hover fieldset': {
                                                            borderColor: 'black',
                                                        },
                                                        '&.Mui-focused fieldset': {
                                                            borderColor: 'black',
                                                        },
                                                    },
                                                    '& .MuiInputLabel-root': {
                                                        color: 'rgba(0, 0, 0, 0.6)',
                                                    },
                                                    '& .MuiInputLabel-root.Mui-focused': {
                                                        color: 'black',
                                                    },
                                                }}
                                            >
                                                <InputLabel id="category-search-label">{t('categorySearch')}</InputLabel>
                                                <Select
                                                    labelId="category-search-label"
                                                    value={selectedSearch}
                                                    onChange={(e) => setSelectedSearch(e.target.value as string)}
                                                    disabled={fav}
                                                    label={t('categorySearch')}
                                                >
                                                    {searchOptions.map((option) => (
                                                        <MenuItem key={option.value} value={option.value}>
                                                            {option.label}
                                                        </MenuItem>
                                                    ))}
                                                </Select>
                                            </StyledFormControl>
                                        </Grid>
                                        <Grid item xs={12} lg={6}>
                                            <StyledTextField
                                                label={t('searchItem')}
                                                value={searchItem}
                                                onChange={(e) => setSearchItem(e.target.value)}
                                                disabled={fav}
                                                fullWidth
                                                onKeyUp={(e) => {
                                                    if (e.key === 'Enter') navigateToSearchResults();
                                                }}
                                                InputLabelProps={{
                                                    shrink: true,
                                                }}
                                                InputProps={{
                                                    endAdornment: (
                                                        <IconButton
                                                            onClick={handleClear}
                                                            disabled={fav}
                                                            edge="end"
                                                        >
                                                            <Clear />
                                                        </IconButton>
                                                    ),
                                                }}
                                            />
                                        </Grid>
                                    </Grid>
                                </Grid>

                                <Grid item xs={12} lg={6}>
                                    <Grid container spacing={2}>
                                        <Grid item xs={12}>
                                            <Grid container justifyContent="space-between" spacing={2}>
                                                <Grid item xs={4} lg={2}>
                                                    <Button
                                                        style={{ height: '100%' }}
                                                        variant="contained"
                                                        onClick={navigateToSearchResults}
                                                        disabled={fav}
                                                        fullWidth
                                                    >
                                                        {t('search')}
                                                    </Button>
                                                </Grid>
                                                <Grid item container xs={8} lg={10} justifyContent="flex-end" spacing={2}>
                                                    <Grid item xs={6} lg={4}>
                                                        <StyledFormControl fullWidth
                                                                           sx={{
                                                                               '& .MuiOutlinedInput-root': {
                                                                                   '& fieldset': {
                                                                                       borderColor: 'rgba(0, 0, 0, 0.23)',
                                                                                   },
                                                                                   '&:hover fieldset': {
                                                                                       borderColor: 'black',
                                                                                   },
                                                                                   '&.Mui-focused fieldset': {
                                                                                       borderColor: 'black',
                                                                                   },
                                                                               },
                                                                               '& .MuiInputLabel-root': {
                                                                                   color: 'rgba(0, 0, 0, 0.6)',
                                                                               },
                                                                               '& .MuiInputLabel-root.Mui-focused': {
                                                                                   color: 'black',
                                                                               },
                                                                           }}>
                                                            <InputLabel id="language-select-label">{t('language')}</InputLabel>
                                                            <Select
                                                                labelId="language-select-label"
                                                                value={selectedLanguage}
                                                                onChange={(e) => updateLangAndSelectedLanguage(e.target.value as string)}
                                                                disabled={fav}
                                                                label={t('language')}
                                                                renderValue={(value) => {
                                                                    if (!value) return '';
                                                                    return getLanguageDisplaySafe(value as string);
                                                                }}
                                                            >
                                                                {supportedLanguages.map((language) => (
                                                                    <MenuItem key={language} value={language}>
                                                                        {getLanguageDisplaySafe(language)}
                                                                    </MenuItem>
                                                                ))}
                                                            </Select>
                                                        </StyledFormControl>
                                                    </Grid>
                                                    <Grid item xs={3} lg={2} style={{ display: 'flex', justifyContent: 'center', alignItems: 'center' }}>
                                                        <IconButton
                                                            onClick={() => setFavContext(!fav)}
                                                            color="default"
                                                            disabled={!favEnabled}
                                                        >
                                                            {fav ? <Favorite /> : <FavoriteBorder />}
                                                        </IconButton>
                                                    </Grid>
                                                </Grid>
                                            </Grid>
                                        </Grid>
                                    </Grid>
                                </Grid>
                            </Grid>
                        </CardContent>
                    </Card>
                </Box>
            </Grid>
        </Grid>
    );
};

export default SearchBar;