import React, { useState, useEffect, useRef } from 'react';
import {
    Box,
    Grid,
    Card,
    CardContent,
    Button,
    Select,
    MenuItem,
    InputLabel,
    SelectChangeEvent,
    IconButton
} from '@mui/material';
import { StyledTextField } from "../StyledDataItems";
import { useTranslation } from 'react-i18next';
import { useAuth } from "../../context/AuthContext";
import { fetchWithAuth } from "../../api/config";
import { Clear, Favorite, FavoriteBorder } from "@mui/icons-material";
import { useNavigate, useLocation } from 'react-router-dom';
import { useFav } from "../../context/FavContext";
import { useAuthor } from "../../context/AuthorContext";
import { useSearchBar } from "../../context/SearchBarContext";
import { StyledFormControl} from "../StyledDataItems";


interface LangItem {
    language: string;
    count: number;
}

interface Record {
    option: string;
    path: string;
}

const SearchBar: React.FC = () => {
    const { user, updateUser } = useAuth();
    const { t } = useTranslation();
    const { languages, searchItem, setLanguages, setSearchItem, selectedSearch, setSelectedSearch } = useSearchBar();
    const [lang, setLang] = useState<string | null>(user?.books_lang || '');
    const navigate = useNavigate();
    const { fav, setFav } = useFav();
    const prevFavRef = useRef(fav);
    const [, setTranslatedOptions] = useState<Array<{ value: string; label: string }>>([]);
    const location = useLocation();
    const [searchOptions, setSearchOptions] = useState<Array<{ value: string; label: string }>>([
        { value: 'title', label: t('byTitle') },
        { value: 'author', label: t('byAuthor') },
    ]);
    const records: Record[] = [
        { option: 'authorsBookSearch', path: `/books/find/author/` },
        { option: 'title', path: `/books/find/title/${searchItem}/1` },
        { option: 'author', path: `/authors/${searchItem}/1` },
    ];
    const {authorId, setAuthorBook, clearAuthorId, clearAuthorBook } = useAuthor();

    useEffect(() => {
        setTranslatedOptions([
            { value: 'title', label: t('byTitle') },
            { value: 'author', label: t('byAuthor') },
        ]);
    }, [t, setTranslatedOptions]);

    useEffect(() => {
        const fetchLangs = async () => {
            try {
                const response = await fetchWithAuth.get('/books/langs');
                if (response.status === 200) {
                    const data = response.data;
                    const languageList = data.langs.map((item: LangItem) => item.language);
                    setLanguages(languageList);
                } else {
                    console.error('Failed to fetch languages');
                }
            } catch (error) {
                console.error('Error fetching languages', error);
            }
        };
        fetchLangs().then(r => r);

        // Set language from user settings
        if (user) {
            setLang(user.books_lang || '');
        }

        // Update URL based on fav state
        if (prevFavRef.current !== fav) {
            const newPath = fav ? '/books/favorite/1' : '/books/page/1';
            navigate(newPath);
        }
        prevFavRef.current = fav;
    }, [user, fav, navigate, setLanguages]);

    useEffect(() => {
        const pathStartsWith = '/books/find/author/';
        const optionValue = 'authorsBookSearch';
        const optionLabel = t('authorsBookSearch');
        const isAuthorsBooksSearchPage = location.pathname.startsWith(pathStartsWith);

        setSearchOptions(prevOptions => {
            const optionExists = prevOptions.some(option => option.value === optionValue);
            if (isAuthorsBooksSearchPage && !optionExists) {
                setSelectedSearch(optionValue);
                return [...prevOptions, {value: optionValue, label: optionLabel}];
            } else if (location.pathname.startsWith('/authors/') && !optionExists) {
                return prevOptions.filter(option => option.value !== optionValue);
            } else if (!isAuthorsBooksSearchPage && optionExists) {
                setSelectedSearch('title');
                return prevOptions.filter(option => option.value !== optionValue);
            }
            return prevOptions;
        });
    }, [location.pathname, t, selectedSearch, clearAuthorId, setSelectedSearch]);

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


    const handleLangChange = (event: SelectChangeEvent) => {
        setLang(event.target.value as string)

        // Ensure user is not null before updating its property and sending it to the backend
        if (user) {
            // Update user data in context
            user.books_lang = event.target.value as string;
            updateUser(user);

            fetchWithAuth.post('/books/change-me', user)
                .then(response => {
                    if (response.status === 200) {
                        console.log('Language updated successfully');
                    } else {
                        console.error('Failed to update language');
                    }
                })
                .catch(error => {
                    console.error('Error updating language', error);
                });
        }

        const pathSegments = location.pathname.split('/');
        const lastIndex = pathSegments.length - 1;
        pathSegments[lastIndex] = '1';
        navigate(pathSegments.join('/'));
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
                                        <Grid item xs={6} lg={6}>
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
                                        <Grid item xs={6} lg={6}>
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
                                                                value={lang || ''}
                                                                onChange={handleLangChange}
                                                                disabled={fav}
                                                                label={t('language')}
                                                            >
                                                                {languages.map((language) => (
                                                                    <MenuItem key={language} value={language}>{language}</MenuItem>
                                                                ))}
                                                            </Select>
                                                        </StyledFormControl>
                                                    </Grid>
                                                    <Grid item xs={3} lg={2} style={{ display: 'flex', justifyContent: 'center', alignItems: 'center' }}>
                                                        <IconButton
                                                            onClick={() => setFavContext(!fav)}
                                                            color="default"
                                                            disabled={!user?.have_favs}
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
