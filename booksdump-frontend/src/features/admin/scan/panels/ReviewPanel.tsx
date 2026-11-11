import React from 'react';
import { useTranslation } from 'react-i18next';

import { Badge } from '@/shared/ui/badge';
import { cn } from '@/shared/lib/utils';
import AuthorReviewQueue, { ReviewQueueTools } from '@/features/admin/AuthorReviewQueue';
import type { Pipeline, StageKey } from '../pipeline';
import ProblemStrip from '../ProblemStrip';
import { useScanSource } from '../ScanSources';
import { formatCount, StagePanel, WARNING_TEXT } from '../scanUi';

/*
 * «Проверка человеком»: the names no rule and no model decided, waiting for a
 * person. The header holds the badge (the important items the queue counts,
 * the singles behind their own filter as quieter text; the run's open count
 * until the counts answer), the view pair, the sort and the open/closed
 * pair; the body is the triage list itself, with no card of its own.
 */

interface ReviewPanelProps {
    pipeline: Pipeline;
    onOpen: (stage: StageKey) => void;
}

const ReviewPanel: React.FC<ReviewPanelProps> = ({ pipeline, onOpen }) => {
    const { t } = useTranslation();
    const node = pipeline.nodes.human;
    const counts = useScanSource('reviewCounts').data;
    return (
        <StagePanel
            stage="human"
            state={node.state}
            badge={
                node.figure === null ? undefined : (
                    <span data-review-badge className="inline-flex items-baseline gap-2">
                        <Badge
                            variant="secondary"
                            className={cn('tabular-nums', node.figure > 0 && WARNING_TEXT)}
                        >
                            {formatCount(node.figure)}
                        </Badge>
                        {counts !== null && counts.singles > 0 && (
                            <span className="text-xs font-normal whitespace-nowrap text-muted-foreground tabular-nums">
                                {t('authorReview.view.singlesCount', {
                                    singles: formatCount(counts.singles),
                                })}
                            </span>
                        )}
                    </span>
                )
            }
            aside={<ReviewQueueTools />}
            strips={<ProblemStrip pipeline={pipeline} current="human" onOpen={onOpen} />}
        >
            <AuthorReviewQueue tools={false} counts={counts} />
        </StagePanel>
    );
};

export default ReviewPanel;
