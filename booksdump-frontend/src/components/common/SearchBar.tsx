import React, { useState, useEffect, useRef } from 'react';
import {
    Box,
    Grid,
    Card,
    CardContent,
    TextField,
    Button,
    Select,
    MenuItem,
    InputLabel,
    FormControl,
    SelectChangeEvent,
    IconButton
} from '@mui/material';
import { useTranslation } from 'react-i18next';
import { useAuth } from "../../context/AuthContext";
import { API_URL } from "../../api/config";
import { Favorite, FavoriteBorder } from "@mui/icons-material";
import { useNavigate, useLocation } from 'react-router-dom';
import { useFav } from "../../context/FavContext";


interface LangItem {
    language: string;
    count: number;
}

const SearchBar: React.FC = () => {
    const { user, token } = useAuth();
    const { t } = useTranslation();
    const [searchItem, setSearchItem] = useState('');
    const [langs, setLangs] = useState<string[]>([]);
    const [lang, setLang] = useState<string | null>(user?.books_lang || '');
    const navigate = useNavigate();
    const { fav, setFav } = useFav();
    const prevFavRef = useRef(fav);
    const location = useLocation();
    const [selectedSearch, setSelectedSearch] = useState<string>('title');
    const [searchOptions, setSearchOptions] = useState<Array<{ value: string; label: string }>>([
        { value: 'title', label: t('byTitle') },
        { value: 'author', label: t('byAuthor') },
    ]);


    useEffect(() => {
        const fetchLangs = async () => {
            const response = await fetch(`${API_URL}/books/langs`, {
                method: 'GET',
                headers: {
                    'Content-Type': 'application/json',
                    Authorization: `${token}`,
                },
            });
            if (response.ok) {
                const data = await response.json();
                const languageList = data.langs.map((item: LangItem) => item.language);
                setLangs(languageList);
            } else {
                console.error('Failed to fetch languages');
            }
        };
        fetchLangs();

        // Set language from user settings
        if (user) {
            setLang(user.books_lang || '');
        }

        // Update URL based on fav state
        if (prevFavRef.current !== fav) {
            const newPath = fav ? '/books/favorite/1' : '/books/page/1';
            navigate(newPath);
        }
        // Обновление предыдущего значения fav на текущее
        prevFavRef.current = fav;
    }, [token, user, fav, navigate]); // Include fav and navigate in the dependency array

    useEffect(() => {
        const pathStartsWith = '/books/find/author/';
        const optionValue = 'authorsBookSearch';
        const optionLabel = t('authorsBookSearch');
        const isAuthorsBooksSearchPage = location.pathname.startsWith(pathStartsWith);

        setSearchOptions(prevOptions => {
            const optionExists = prevOptions.some(option => option.value === optionValue);

            if (isAuthorsBooksSearchPage && !optionExists) {
                setSelectedSearch(optionValue);
                return [...prevOptions, { value: optionValue, label: optionLabel }];
            } else if (!isAuthorsBooksSearchPage && optionExists) {
                setSelectedSearch(location.pathname.includes('/books/authors/') ? 'author' : 'title');
                return prevOptions.filter(option => option.value !== optionValue);
            }

            return prevOptions;
        });
    }, [location.pathname, t]);



    const setFavContext = (fav: boolean) => {
        setFav(fav);
    }

    const findByTitle = () => {
        navigate(`/books/find?category=${selectedSearch}&query=${searchItem}&lang=${lang}`);
    };

    const handleLangChange = (event: SelectChangeEvent) => {
        setLang(event.target.value as string);
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
                                            <FormControl
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
                                            </FormControl>
                                        </Grid>
                                        <Grid item xs={6} lg={6}>
                                            <TextField
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
                                                label={t('searchItem')}
                                                value={searchItem}
                                                onChange={(e) => setSearchItem(e.target.value)}
                                                disabled={fav}
                                                fullWidth
                                                onKeyUp={(e) => {
                                                    if (e.key === 'Enter') findByTitle();
                                                }}
                                                InputLabelProps={{
                                                    shrink: true,
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
                                                        onClick={findByTitle}
                                                        disabled={fav}
                                                        fullWidth
                                                    >
                                                        {t('search')}
                                                    </Button>
                                                </Grid>
                                                <Grid item container xs={8} lg={10} justifyContent="flex-end" spacing={2}>
                                                    <Grid item xs={6} lg={4}>
                                                        <FormControl fullWidth
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
                                                                {langs.map((language) => (
                                                                    <MenuItem key={language} value={language}>{language}</MenuItem>
                                                                ))}
                                                            </Select>
                                                        </FormControl>
                                                    </Grid>
                                                    <Grid item xs={3} lg={2} style={{ display: 'flex', justifyContent: 'center', alignItems: 'center' }}>
                                                        <IconButton
                                                            onClick={() => setFavContext(!fav)}
                                                            color="default"
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
