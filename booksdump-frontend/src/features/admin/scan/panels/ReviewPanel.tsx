import React from 'react';

import { Badge } from '@/shared/ui/badge';
import { cn } from '@/shared/lib/utils';
import AuthorReviewQueue, { ReviewQueueTools } from '@/features/admin/AuthorReviewQueue';
import type { Pipeline, StageKey } from '../pipeline';
import ProblemStrip from '../ProblemStrip';
import { formatCount, StagePanel, WARNING_TEXT } from '../scanUi';

/*
 * «Проверка человеком»: the names no rule and no model decided, waiting for a
 * person. The header holds the badge (the open count the metadata run reports,
 * which the queue's pages do not), the sort and the open/closed pair; the body
 * is the triage list itself, with no card of its own.
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
            aside={<ReviewQueueTools />}
            strips={<ProblemStrip pipeline={pipeline} current="human" onOpen={onOpen} />}
        >
            <AuthorReviewQueue tools={false} openCount={node.figure} />
        </StagePanel>
    );
};

export default ReviewPanel;
