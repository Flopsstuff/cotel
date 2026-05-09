import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router-dom'
import './styles/tokens.css'
import './styles/global.css'
import { Layout } from './components'
import Overview from './pages/Overview'
import Sessions from './pages/Sessions'
import SessionDetail from './pages/SessionDetail'
import Costs from './pages/Costs'
import Tools from './pages/Tools'
import Models from './pages/Models'
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <Layout>
        <Routes>
          <Route path="/" element={<Overview />} />
          <Route path="/sessions" element={<Sessions />} />
          <Route path="/sessions/:id" element={<SessionDetail />} />
          <Route path="/costs" element={<Costs />} />
          <Route path="/tools" element={<Tools />} />
          <Route path="/models" element={<Models />} />
        </Routes>
      </Layout>
    </BrowserRouter>
  </StrictMode>,
)
