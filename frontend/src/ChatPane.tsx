import {useEffect, useRef, useState} from 'react';
import {GetChat, SendChat, StopChat} from "../wailsjs/go/main/App";
import {main} from "../wailsjs/go/models";
import {EventsOn} from "../wailsjs/runtime/runtime";
import {isEnter} from "./Mascot";

// Go 側が会話の 1 件や実行状態の変化を送ってくるイベント
const CHAT_MESSAGE_EVENT = 'chat-message';
const CHAT_STATE_EVENT = 'chat-state';

const ROLE_LABELS: Record<string, string> = {user: 'あなた', assistant: 'Claude'};

// 右端の Claude パネル。開いているディレクトリと Claude Code のセッションは 1 対 1
export function ChatPane({repo, label, onError}: {
    repo: string;
    label: string;
    onError: (e: unknown) => void;
}) {
    const [chat, setChat] = useState<main.ChatState | null>(null);
    const [text, setText] = useState('');
    const listRef = useRef<HTMLDivElement>(null);

    // ディレクトリが変わったら、そのディレクトリのセッションの会話を読み直す
    useEffect(() => {
        setChat(null);
        if (!repo) return;
        GetChat().then(setChat).catch(onError);
    }, [repo]);

    // 届いた会話は、今開いているディレクトリの分だけ足す
    useEffect(() => {
        const offMessage = EventsOn(CHAT_MESSAGE_EVENT, ({dir, message}: { dir: string; message: main.ChatMessage }) => {
            setChat((c) => c && c.dir === dir ? {...c, messages: [...c.messages, message]} as main.ChatState : c);
        });
        const offState = EventsOn(CHAT_STATE_EVENT, ({dir, running}: { dir: string; running: boolean }) => {
            setChat((c) => c && c.dir === dir ? {...c, running} as main.ChatState : c);
        });
        return () => {
            offMessage();
            offState();
        };
    }, []);

    useEffect(() => {
        listRef.current?.scrollTo({top: listRef.current.scrollHeight});
    }, [chat?.messages.length]);

    const send = () => {
        const message = text.trim();
        if (!message || chat?.running) return;
        SendChat(message).then(() => setText('')).catch(onError);
    };

    return (
        <section className="chat-pane">
            <h2 title={chat ? `セッション ${chat.sessionId}\n${chat.dir}` : undefined}>
                {repo && label ? `Claude — ${label}` : 'Claude'}
            </h2>
            <div className="chat-messages" ref={listRef}>
                {!repo && (
                    <p className="chat-empty">リポジトリを選ぶと、そのリポジトリ専用の Claude と話せます。</p>
                )}
                {chat?.messages.length === 0 && (
                    <p className="chat-empty">このディレクトリ専用の Claude との会話です。auto モードなので、安全な操作は自動で許可し、危なそうな操作は断ります。</p>
                )}
                {chat?.messages.map((m, i) => (
                    <div key={i} className={`chat-message chat-${m.role}`}>
                        {ROLE_LABELS[m.role] && <span className="chat-role">{ROLE_LABELS[m.role]}</span>}
                        {m.text}
                    </div>
                ))}
                {chat?.running && (
                    <div className="chat-message chat-thinking">
                        <span className="spinner"/>考えています…
                    </div>
                )}
            </div>
            <div className="chat-input">
                <textarea value={text} rows={3} placeholder="Claude に頼む（⌘ + Enter で送信）" disabled={!chat}
                          onChange={(e) => setText(e.target.value)}
                          onKeyDown={(e) => {
                              if (isEnter(e) && (e.metaKey || e.ctrlKey)) send();
                          }}/>
                {chat?.running
                    ? <button className="chat-stop" onClick={() => StopChat().catch(onError)}>止める</button>
                    : <button onClick={send} disabled={!chat || !text.trim()}>送信</button>}
            </div>
        </section>
    );
}
