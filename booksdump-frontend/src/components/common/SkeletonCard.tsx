import { Box, Card } from '@mui/material';
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
            <Box sx={{ width: '100%' }}>
                <LinearIndeterminate />
            </Box>
        </Card>
    );
};

export default SkeletonCard;