import { Box, Card, Grid } from '@mui/material';
import LinearProgress from '@mui/material/LinearProgress';

function LinearIndeterminate() {
    return (
        <Box sx={{ width: '100%' }}>
            <LinearProgress color={'secondary'} />
        </Box>
    );
}

const SkeletonCard: React.FC = () => {
    return (
        // Margin top and bottom 2
        <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                <Grid item xs={12} md={12}>
                    <LinearIndeterminate />
                </Grid>
        </Card>
    );
};

export default SkeletonCard;