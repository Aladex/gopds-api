import React from 'react';

import { Badge } from '@/shared/ui/badge';
import { cn } from '@/shared/lib/utils';
import AuthorReviewQueue from '@/features/admin/AuthorReviewQueue';
import type { Pipeline, StageKey } from '../pipeline';
import ProblemStrip from '../ProblemStrip';
import { formatCount, StagePanel, WARNING_TEXT } from '../scanUi';

/*
 * «Проверка человеком»: the names no rule and no model decided, waiting for a
 * person. It hosts the review queue as it is; the badge is the open count the
 * metadata run reports, which the queue's pages do not.
 */

interface ReviewPanelProps {
    pipeline: Pipeline;
    onOpen: (stage: StageKey) => void;
}

const ReviewPanel: React.FC<ReviewPanelProps> = ({ pipeline, onOpen }) => {
    const node = pipeline.nodes.human;
    return (
        <StagePanel
            stage="human"
            state={node.state}
            badge={
                node.figure === null ? undefined : (
                    <Badge
                        variant="secondary"
                        className={cn('tabular-nums', node.figure > 0 && WARNING_TEXT)}
                    >
                        {formatCount(node.figure)}
                    </Badge>
                )
            }
            strips={<ProblemStrip pipeline={pipeline} current="human" onOpen={onOpen} />}
        >
            <AuthorReviewQueue />
        </StagePanel>
    );
};

export default ReviewPanel;
