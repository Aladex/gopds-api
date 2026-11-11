import React from 'react';
import { useTranslation } from 'react-i18next';

import { Badge } from '@/shared/ui/badge';
import AuthorEntityBlock from '@/features/admin/AuthorEntityBlock';
import type { Pipeline, StageKey } from '../pipeline';
import ProblemStrip from '../ProblemStrip';
import { useScanSource } from '../ScanSources';
import { formatCount, StagePanel } from '../scanUi';

/*
 * «Сущности»: the author entities. The entity block is the panel's body; the
 * header carries the entity count and how many merges the model made.
 */

interface EntitiesPanelProps {
    pipeline: Pipeline;
    onOpen: (stage: StageKey) => void;
}

const EntitiesPanel: React.FC<EntitiesPanelProps> = ({ pipeline, onOpen }) => {
    const { t } = useTranslation();
    const merges = useScanSource('merges').data;
    const node = pipeline.nodes.ent;
    return (
        <StagePanel
            stage="ent"
            state={node.state}
            badge={
                node.figure === null ? undefined : (
                    <Badge variant="secondary" className="tabular-nums">
                        {formatCount(node.figure)}
                    </Badge>
                )
            }
            aside={
                merges !== null && (
                    <span className="text-xs text-muted-foreground tabular-nums">
                        {t('scanPanel.ent.merges', {
                            count: merges.decisions_total,
                            merges: formatCount(merges.decisions_total),
                        })}
                    </span>
                )
            }
            strips={<ProblemStrip pipeline={pipeline} current="ent" onOpen={onOpen} />}
        >
            <AuthorEntityBlock variant="panel" />
        </StagePanel>
    );
};

export default EntitiesPanel;
