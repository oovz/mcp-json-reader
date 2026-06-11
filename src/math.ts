/**
 * Evaluates basic arithmetic operations on a number securely without using eval or new Function.
 */
export function safeEvaluateMath(num: number, expr: string): number {
    const allowed = /^[0-9\+\-\*\/\s\.()]+$/;
    if (!allowed.test(expr)) return 0;

    const fullExpr = `${num} ${expr}`;
    
    // Tokenizer supporting unary signs
    const tokens: string[] = [];
    let i = 0;
    while (i < fullExpr.length) {
        const char = fullExpr[i];
        if (/\s/.test(char)) {
            i++;
            continue;
        }
        if (char === '(' || char === ')') {
            tokens.push(char);
            i++;
            continue;
        }
        if (char === '+' || char === '-') {
            const lastToken = tokens[tokens.length - 1];
            const isUnary = tokens.length === 0 || 
                            lastToken === '(' || 
                            ['+', '-', '*', '/'].includes(lastToken);
            if (isUnary) {
                let nextIndex = i + 1;
                while (nextIndex < fullExpr.length && /\s/.test(fullExpr[nextIndex])) {
                    nextIndex++;
                }
                if (fullExpr[nextIndex] === '(') {
                    if (char === '-') {
                        tokens.push('-1');
                        tokens.push('*');
                    }
                    i = nextIndex;
                    continue;
                } else {
                    let numStr = char;
                    i++;
                    while (i < fullExpr.length && /[0-9\.]/.test(fullExpr[i])) {
                        numStr += fullExpr[i];
                        i++;
                    }
                    tokens.push(numStr);
                    continue;
                }
            }
        }
        if (['+', '-', '*', '/'].includes(char)) {
            tokens.push(char);
            i++;
            continue;
        }
        if (/[0-9\.]/.test(char)) {
            let numStr = '';
            while (i < fullExpr.length && /[0-9\.]/.test(fullExpr[i])) {
                numStr += fullExpr[i];
                i++;
            }
            tokens.push(numStr);
            continue;
        }
        i++;
    }

    if (tokens.length === 0) return 0;

    const outputQueue: number[] = [];
    const operatorStack: string[] = [];
    const precedence: Record<string, number> = {
        '+': 1,
        '-': 1,
        '*': 2,
        '/': 2
    };

    const applyOp = (op: string, l: number, r: number): number => {
        switch (op) {
            case '+': return l + r;
            case '-': return l - r;
            case '*': return l * r;
            case '/': return r !== 0 ? l / r : 0;
            default: return 0;
        }
    };

    for (const token of tokens) {
        if (/^-?\d+(?:\.\d+)?$/.test(token)) {
            outputQueue.push(parseFloat(token));
        } else if (token in precedence) {
            while (
                operatorStack.length > 0 &&
                operatorStack[operatorStack.length - 1] !== '(' &&
                precedence[operatorStack[operatorStack.length - 1]] >= precedence[token]
            ) {
                const op = operatorStack.pop()!;
                const r = outputQueue.pop();
                const l = outputQueue.pop();
                if (l === undefined || r === undefined) return 0;
                outputQueue.push(applyOp(op, l, r));
            }
            operatorStack.push(token);
        } else if (token === '(') {
            operatorStack.push(token);
        } else if (token === ')') {
            while (operatorStack.length > 0 && operatorStack[operatorStack.length - 1] !== '(') {
                const op = operatorStack.pop()!;
                const r = outputQueue.pop();
                const l = outputQueue.pop();
                if (l === undefined || r === undefined) return 0;
                outputQueue.push(applyOp(op, l, r));
            }
            if (operatorStack.length === 0) return 0;
            operatorStack.pop();
        }
    }

    while (operatorStack.length > 0) {
        const op = operatorStack.pop()!;
        if (op === '(' || op === ')') return 0;
        const r = outputQueue.pop();
        const l = outputQueue.pop();
        if (l === undefined || r === undefined) return 0;
        outputQueue.push(applyOp(op, l, r));
    }

    if (outputQueue.length !== 1) return 0;
    return outputQueue[0];
}
