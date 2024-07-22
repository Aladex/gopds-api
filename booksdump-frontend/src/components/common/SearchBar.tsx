// src/components/SearchBar.tsx
import React, {useState, useEffect} from 'react';
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
    SelectChangeEvent, IconButton
} from '@mui/material';
import {useTranslation} from 'react-i18next';
import {useAuth} from "../../context/AuthContext";
import {API_URL} from "../../api/config";
import {Stars, StarBorderOutlined} from "@mui/icons-material";

interface LangItem {
    language: string;
    count: number;
}

const SearchBar: React.FC = () => {
    const { user, token } = useAuth();
    const {t} = useTranslation();
    const [selectedSearch, setSelectedSearch] = useState<string | null>(null);
    const [searchItem, setSearchItem] = useState('');
    const [fav, setFav] = useState(false);
    const [langs, setLangs] = useState<string[]>([]);
    const [lang, setLang] = useState<string | null>(user?.books_lang || '');
    const [authorName, setAuthorName] = useState<string>('');

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
    }, [token, user]);

    const findByTitle = () => {
        // Implement search logic here
        console.log('Searching for:', searchItem, 'in category:', selectedSearch, 'and language:', lang);
    };

    const handleLangChange = (event: SelectChangeEvent) => {
        setLang(event.target.value as string);
        // Implement any additional logic on language change
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
                                                    value={selectedSearch || 'title'}
                                                    onChange={(e) => setSelectedSearch(e.target.value as string)}
                                                    disabled={fav}
                                                    label={t('categorySearch')}
                                                >
                                                    <MenuItem value="title">{t('byTitle')}</MenuItem>
                                                    <MenuItem value="author">{t('byAuthor')}</MenuItem>
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
                                                        <IconButton onClick={() => setFav(!fav)} color="default" >
                                                            {fav ? <Stars /> : <StarBorderOutlined />}
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
