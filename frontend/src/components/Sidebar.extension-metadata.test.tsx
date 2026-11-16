import { act, render, screen } from '@testing-library/preact';
import { afterEach, describe, expect, it } from 'vitest';
import { StoreContext } from '../app/context';
import type { Session } from '../domain/types';
import { AppStore } from '../stores/app-store';
import { testConfig, testSession } from '../stores/store-test-fixtures';
import { Sidebar } from './Sidebar';

let store: AppStore;
afterEach(() => store?.dispose());

function show(sessions: Session[]) {
  store = new AppStore(testConfig);
  store.sessions.value = sessions;
  return render(
    <StoreContext.Provider value={store}>
      <Sidebar />
    </StoreContext.Provider>,
  );
}

function row(title: string) {
  return screen.getByRole('button', { name: title }).closest('.session-row')!;
}

describe('sidebar extension metadata', () => {
  it('identifies same-title pinned and ordinary chats by session ID and recorded agent', () => {
    const { container } = show([
      testSession({ id: 'pinned-chat', title: 'Same title', pinned: true, agent: 'developer' }),
      testSession({ id: 'regular-chat', title: 'Same title', agent: 'codebase' }),
    ]);
    const rows = container.querySelectorAll('.session-row');
    expect(rows).toHaveLength(2);
    const pinned = container.querySelector('[data-session-id="pinned-chat"]');
    const regular = container.querySelector('[data-session-id="regular-chat"]');
    expect(pinned).toHaveAttribute('data-session-agent', 'developer');
    expect(pinned).toHaveAttribute('data-reorder-id', 'pinned-chat');
    expect(regular).toHaveAttribute('data-session-agent', 'codebase');
    expect(regular).not.toHaveAttribute('data-reorder-id');
    expect(screen.getAllByRole('button', { name: 'Same title' })).toHaveLength(2);
  });

  it('does not infer an agent from the selected new-chat agent', () => {
    show([
      testSession({ id: 'known', title: 'Recorded chat', agent: 'developer' }),
      testSession({ id: 'unknown', title: 'Unrecorded chat' }),
      testSession({ id: 'blank', title: 'Empty agent chat', agent: '' }),
    ]);
    act(() => {
      store.selectedAgent.value = 'reviewer';
    });
    expect(row('Recorded chat')).toHaveAttribute('data-session-agent', 'developer');
    expect(row('Unrecorded chat')).toHaveAttribute('data-session-id', 'unknown');
    expect(row('Unrecorded chat')).not.toHaveAttribute('data-session-agent');
    expect(row('Empty agent chat')).toHaveAttribute('data-session-id', 'blank');
    expect(row('Empty agent chat')).not.toHaveAttribute('data-session-agent');
  });

  it('updates and removes agent metadata with the rendered session', () => {
    const session = testSession({ id: 'changing', title: 'Changing chat', agent: 'developer' });
    show([session]);
    act(() => {
      store.sessions.value = [{ ...session, agent: 'codebase' }];
    });
    expect(row('Changing chat')).toHaveAttribute('data-session-id', 'changing');
    expect(row('Changing chat')).toHaveAttribute('data-session-agent', 'codebase');
    act(() => {
      store.sessions.value = [{ ...session, agent: '' }];
    });
    expect(row('Changing chat')).not.toHaveAttribute('data-session-agent');
  });

  it('also exposes metadata on project and search-result rows', () => {
    show([]);
    const projectChat = testSession({
      id: 'project-chat',
      title: 'Project chat',
      projectId: 'project',
      agent: 'developer',
    });
    act(() => {
      store.projectsEnabled.value = true;
      store.setSidebarView('projects');
      store.projects.value = [{ id: 'project', name: 'Example project', sessions: [projectChat] }];
    });
    expect(row('Project chat')).toHaveAttribute('data-session-id', 'project-chat');
    expect(row('Project chat')).toHaveAttribute('data-session-agent', 'developer');
    act(() => {
      store.searchResults.value = [
        testSession({ id: 'search-chat', title: 'Search chat', agent: 'work' }),
      ];
    });
    expect(row('Search chat')).toHaveAttribute('data-session-id', 'search-chat');
    expect(row('Search chat')).toHaveAttribute('data-session-agent', 'work');
  });

  it('keeps markup-like agent names as literal attribute values', () => {
    const agent = '"><img src=x onerror="alert(1)"><script>alert(1)</script>';
    const { container } = show([testSession({ id: 'literal', title: 'Literal chat', agent })]);
    expect(row('Literal chat')).toHaveAttribute('data-session-agent', agent);
    expect(container.querySelector('img[src="x"], script, [onerror]')).toBeNull();
  });
});
