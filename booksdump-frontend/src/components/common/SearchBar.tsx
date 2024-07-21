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
import {StarBorder, StarBorderOutlined} from "@mui/icons-material";

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
    const [lang, setLang] = useState<string | null>(null);
    const [authorName, setAuthorName] = useState<string>('');

    useEffect(() => {
        const fetchLangs = async () => {
            // Get languages from the server /api/books/langs
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
    }, [token]);

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
                    <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                        <CardContent>
                            <Grid container spacing={2} justifyContent="start">
                                {/* First column: Category, Search Field, and Button */}
                                <Grid item xs={6} lg={6}>
                                    <Grid container spacing={2}>
                                        <Grid item xs={4}>
                                            <FormControl fullWidth>
                                                <InputLabel id="category-search-label">{t('categorySearch')}</InputLabel>
                                                <Select
                                                    labelId="category-search-label"
                                                    value={selectedSearch || 'title'}
                                                    onChange={(e) => setSelectedSearch(e.target.value as string)}
                                                    disabled={fav}
                                                    label={t('categorySearch')}
                                                >
                                                    <MenuItem value="title">{t('title')}</MenuItem>
                                                    <MenuItem value="author">{t('author')}</MenuItem>
                                                </Select>
                                            </FormControl>
                                        </Grid>
                                        <Grid item xs={4}>
                                            <TextField
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
                                        <Grid item xs={4}>
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
                                    </Grid>
                                </Grid>

                                {/* Empty column for spacing */}
                                <Grid item xs={1} lg={1}></Grid>

                                {/* Second column: Language and Favorites */}
                                <Grid item xs={5} lg={5}>
                                    <Grid container spacing={2} justifyContent="flex-end">
                                        <Grid item xs={3}>
                                            <FormControl fullWidth>
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
                                        <Grid item xs={3}>
                                            <IconButton onClick={() => setFav(!fav)} color="default">
                                                {fav ? <StarBorder /> : <StarBorderOutlined />}
                                            </IconButton>
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
